#!/usr/bin/env node
/**
 * generate-openapi.js — OpenAPI 3.0.1 generator for the Go microservices
 * ======================================================================
 *
 * A single-file, zero-dependency scanner that reads the Gin routers
 * (cmd/router.go) and the handler packages of every microservice in the
 * monorepo and generates an OpenAPI 3.0.1 specification.
 *
 * What it extracts:
 *   - HTTP verb + path (Gin `:param` segments become `{param}` path params)
 *   - Security model: public / JWT (Bearer) / internal (X-Internal-Service-Token)
 *   - RBAC permission scopes from `middleware.RequirePermission(...)`
 *   - Path params (`c.Param("x")`) and query params (`c.Query("x")`,
 *     `c.QueryArray("x")`, `c.DefaultQuery("x", d)`)
 *   - Request bodies from `c.ShouldBindJSON(&req)` + the struct's `json` /
 *     `binding` tags (required, enum/oneof, min/max, email, ...)
 *   - Response envelope (`httputil.StandardResponse`) with the success data
 *     type inferred from the handler, plus error status codes
 *   - Handler doc comments as `summary` / `description`
 *
 * Output:
 *   docs/openapi/<service>.yaml   one spec per service (default)
 *   docs/openapi/openapi.yaml     combined spec for the whole fleet (default)
 *
 * Usage:
 *   node scripts/generate-openapi.js                    # write YAML for all
 *   node scripts/generate-openapi.js --json             # write JSON instead
 *   node scripts/generate-openapi.js --service auth     # one service
 *   node scripts/generate-openapi.js --out /tmp/spec    # custom output dir
 *   node scripts/generate-openapi.js --server https://api.example.com
 *   node scripts/generate-openapi.js --no-per-service   # combined only
 *   node scripts/generate-openapi.js --no-combined      # per-service only
 *   node scripts/generate-openapi.js --help
 *
 * Limitations (heuristic, source-code scraper):
 *   - Schema inference is best-effort: fields whose Go type cannot be resolved
 *     to a handler-package struct or a scalar are rendered as opaque objects.
 *   - Routes registered dynamically (loops, config-driven) are not captured.
 *   - The health endpoints use inline Gin handlers and are emitted as a plain
 *     200 with no data envelope.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const REPO = path.resolve(__dirname, '..');

// Services with an HTTP surface. infra-provisioner is a worker and is skipped.
const SERVICES = [
  { name: 'auth-service', title: 'Auth Service', port: 8085 },
  { name: 'user-service', title: 'User Service', port: 8081 },
  { name: 'tenant-service', title: 'Tenant Service', port: 8082 },
  { name: 'order-service', title: 'Order Service', port: 8084 },
  { name: 'notification-service', title: 'Notification Service', port: 8083 },
];

const DEFAULT_OUT = path.join('docs', 'openapi');

const HTTP_STATUS = {
  StatusContinue: 100,
  StatusOK: 200,
  StatusCreated: 201,
  StatusAccepted: 202,
  StatusNoContent: 204,
  StatusMovedPermanently: 301,
  StatusFound: 302,
  StatusBadRequest: 400,
  StatusUnauthorized: 401,
  StatusPaymentRequired: 402,
  StatusForbidden: 403,
  StatusNotFound: 404,
  StatusMethodNotAllowed: 405,
  StatusConflict: 409,
  StatusGone: 410,
  StatusTooManyRequests: 429,
  StatusInternalServerError: 500,
  StatusNotImplemented: 501,
  StatusBadGateway: 502,
  StatusServiceUnavailable: 503,
};

const SUCCESS_DESCRIPTIONS = {
  200: 'Successful response',
  201: 'Resource created',
  202: 'Accepted for processing',
  204: 'No content',
};

const ERROR_DESCRIPTIONS = {
  400: 'Bad Request — invalid request payload or validation failure',
  401: 'Unauthorized — missing or invalid token',
  403: 'Forbidden — authenticated but lacking the required permission',
  404: 'Not Found — the requested resource does not exist',
  405: 'Method Not Allowed',
  409: 'Conflict — the resource already exists',
  429: 'Too Many Requests',
  500: 'Internal Server Error',
  501: 'Not Implemented',
  503: 'Service Unavailable',
};

const SCALAR = new Set([
  'string', 'int', 'int8', 'int16', 'int32', 'int64',
  'uint', 'uint8', 'uint16', 'uint32', 'uint64',
  'float32', 'float64', 'bool', 'byte', 'rune',
  'any', 'object', 'interface{}',
]);

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------
const args = process.argv.slice(2);
const opts = {
  out: DEFAULT_OUT,
  format: 'yaml',
  server: null,
  filter: SERVICES.map((s) => s.name),
  combined: true,
  perService: true,
};

for (let i = 0; i < args.length; i++) {
  switch (args[i]) {
    case '--out':
      opts.out = args[++i];
      break;
    case '--json':
      opts.format = 'json';
      break;
    case '--yaml':
      opts.format = 'yaml';
      break;
    case '--server':
      opts.server = args[++i];
      break;
    case '--service':
      opts.filter = [args[++i]];
      break;
    case '--no-combined':
      opts.combined = false;
      break;
    case '--no-per-service':
      opts.perService = false;
      break;
    case '--help':
    case '-h':
      printHelp();
      process.exit(0);
    default:
      console.error(`Unknown option: ${args[i]} (try --help)`);
      process.exit(2);
  }
}

function printHelp() {
  const p = path.basename(process.argv[1]);
  console.log(`OpenAPI 3.0.1 generator for the Go microservices

Usage:
  node ${p}                     write YAML specs for every service
  node ${p} --json              write JSON specs instead of YAML
  node ${p} --service user      only the given service
  node ${p} --out <dir>         output directory (default: docs/openapi)
  node ${p} --server <url>      override the server URL for every service
  node ${p} --no-per-service    skip per-service files
  node ${p} --no-combined       skip the combined openapi.yaml

Outputs one spec per service plus a combined spec.`);
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

/** Remove // and /* *\/ comments while keeping line structure for //. */
function stripComments(src) {
  let out = '';
  let inStr = null;
  let esc = false;
  for (let i = 0; i < src.length; i++) {
    const ch = src[i];
    const nxt = src[i + 1];
    if (inStr) {
      out += ch;
      if (esc) {
        esc = false;
      } else if (ch === '\\') {
        esc = true;
      } else if (ch === inStr) {
        inStr = null;
      }
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') {
      inStr = ch;
      out += ch;
      continue;
    }
    if (ch === '/' && nxt === '*') {
      i += 2;
      while (i < src.length && !(src[i] === '*' && src[i + 1] === '/')) i++;
      i += 2;
      out += ' ';
      continue;
    }
    if (ch === '/' && nxt === '/') {
      while (i < src.length && src[i] !== '\n') i++;
      out += '\n';
      continue;
    }
    out += ch;
  }
  return out;
}

/** Index of the closing bracket matching src[openIdx]. */
function findMatching(src, openIdx, open, close) {
  let depth = 0;
  let inStr = null;
  let esc = false;
  for (let i = openIdx; i < src.length; i++) {
    const ch = src[i];
    if (inStr) {
      if (esc) {
        esc = false;
      } else if (ch === '\\') {
        esc = true;
      } else if (ch === inStr) {
        inStr = null;
      }
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') {
      inStr = ch;
      continue;
    }
    if (ch === open) depth++;
    else if (ch === close) {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

function findMatchingParen(src, openIdx) {
  return findMatching(src, openIdx, '(', ')');
}

function findMatchingBrace(src, openIdx) {
  return findMatching(src, openIdx, '{', '}');
}

/** Split a call-args string on top-level commas (respects nesting + strings). */
function splitTopLevel(argsStr, sep) {
  const parts = [];
  let depth = 0;
  let start = 0;
  let inStr = null;
  let esc = false;
  for (let i = 0; i < argsStr.length; i++) {
    const ch = argsStr[i];
    if (inStr) {
      if (esc) {
        esc = false;
      } else if (ch === '\\') {
        esc = true;
      } else if (ch === inStr) {
        inStr = null;
      }
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') {
      inStr = ch;
      continue;
    }
    if ('([{'.includes(ch)) depth++;
    else if (')]}'.includes(ch)) depth--;
    else if (ch === sep && depth === 0) {
      parts.push(argsStr.slice(start, i).trim());
      start = i + 1;
    }
  }
  parts.push(argsStr.slice(start).trim());
  return parts;
}

function lineOf(content, index) {
  return content.slice(0, index).split('\n').length;
}

function snakeCase(s) {
  return s.replace(/([a-z0-9])([A-Z])/g, '$1_$2').toLowerCase();
}

function humanize(s) {
  return s.replace(/([a-z0-9])([A-Z])/g, '$1 $2').trim();
}

function relOf(file) {
  return file.slice(REPO.length + 1).replace(/\\/g, '/');
}

function listGoFiles(serviceDir) {
  const out = [];
  (function walk(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.isFile() && entry.name.endsWith('.go')) out.push(full);
    }
  })(serviceDir);
  return out;
}

function shortName(svc) {
  return svc.name.replace(/-service$/, '');
}

function defaultServer(svc) {
  return `http://localhost:${svc.port}`;
}

// ---------------------------------------------------------------------------
// Router parsing
// ---------------------------------------------------------------------------

function parseRouter(routerSrc) {
  const src = stripComments(routerSrc);
  const groups = { r: { prefix: '', use: [] } };

  const groupRe = /([A-Za-z_]\w*)\s*:=\s*r\.Group\(\s*"([^"]+)"\s*\)/g;
  let m;
  while ((m = groupRe.exec(src))) {
    groups[m[1]] = { prefix: m[2], use: [] };
  }

  const useRe = /([A-Za-z_]\w*)\.Use\(/g;
  while ((m = useRe.exec(src))) {
    const varName = m[1];
    if (!groups[varName]) groups[varName] = { prefix: '', use: [] };
    const close = findMatchingParen(src, m.index + m[0].length - 1);
    if (close < 0) continue;
    const body = src.slice(m.index + m[0].length, close);
    groups[varName].use.push(...splitTopLevel(body, ','));
  }

  const routes = [];
  const routeRe = /([A-Za-z_]\w*)\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|Any|Match)\s*\(/g;
  while ((m = routeRe.exec(src))) {
    const varName = m[1];
    const method = m[2];
    const openIdx = m.index + m[0].length - 1;
    const close = findMatchingParen(src, openIdx);
    if (close < 0) continue;
    const args = splitTopLevel(src.slice(openIdx + 1, close), ',');
    const first = args[0] || '';
    const pm = /^"([^"]*)"$/.exec(first);
    if (!pm) continue;
    routes.push({
      varName,
      method: method.toUpperCase(),
      ginPath: pm[1],
      args: args.slice(1),
      line: lineOf(routerSrc, m.index),
    });
  }

  return { groups, routes };
}

function routeSecurity(route, groups) {
  const group = groups[route.varName] || { use: [] };
  const rootUse = (groups.r && groups.r.use) || [];
  const middleware = [...rootUse, ...group.use, ...route.args];
  const perms = [];
  let jwt = false;
  let internal = false;
  for (const tok of middleware) {
    const pm =
      /middleware\.RequirePermission\(\s*"([^"]+)"\s*\)/.exec(tok) ||
      /middleware\.RequirePermission\(\s*([A-Za-z_]\w*)\s*\)/.exec(tok);
    if (pm) perms.push(pm[1]);
    if (/middleware\.RequireJWT\b/.test(tok)) jwt = true;
    if (/middleware\.InternalAuthMiddleware\b/.test(tok)) internal = true;
  }
  let category = 'public';
  if (internal) category = 'internal';
  else if (jwt) category = 'authenticated';
  return { perms: [...new Set(perms)], jwt, internal, category };
}

function handlerRefFromRoute(args) {
  for (let i = args.length - 1; i >= 0; i--) {
    const a = args[i].trim();
    const hf = /^(\w+)\.(\w+)$/.exec(a);
    if (hf) return { varName: hf[1], method: hf[2] };
    if (/^func\s*\(/.test(a)) return { inline: true, method: '' };
  }
  return null;
}

// ---------------------------------------------------------------------------
// Handler / struct indexing
// ---------------------------------------------------------------------------

function indexHandlers(serviceDir) {
  const byMethod = {};
  const structs = {};
  for (const file of listGoFiles(serviceDir)) {
    if (/_test\.go$/.test(file)) continue;
    const content = fs.readFileSync(file, 'utf8');

    const structRe = /type\s+([A-Z]\w*)\s+struct\s*\{/g;
    let sm;
    while ((sm = structRe.exec(content))) {
      const openIdx = content.indexOf('{', sm.index);
      const close = findMatchingBrace(content, openIdx);
      if (close < 0) continue;
      structs[sm[1]] = { body: content.slice(openIdx + 1, close), file };
    }

    const methodRe =
      /\bfunc\s*\(\s*\w+\s+\*?([A-Z]\w*)\s*\)\s+([A-Z]\w*)\s*\(\s*(?:c\s+\*gin\.Context|\w+\s+\*?\w+)\s*\)/g;
    let mm;
    while ((mm = methodRe.exec(content))) {
      const openIdx = content.indexOf('{', mm.index + mm[0].length);
      const close = findMatchingBrace(content, openIdx);
      if (close < 0) continue;
      const methodName = mm[2];
      if (!byMethod[methodName] || /\/internal\/handler\//.test(relOf(file))) {
        byMethod[methodName] = {
          method: methodName,
          receiver: mm[1],
          body: content.slice(openIdx + 1, close),
          source: content,
          file,
          line: lineOf(content, mm.index),
          comment: extractDocComment(content, mm.index),
        };
      }
    }
  }
  return { byMethod, structs };
}

function extractDocComment(content, methodStart) {
  const parts = content.slice(0, methodStart).split('\n');
  const lines = [];
  for (let i = parts.length - 2; i >= 0; i--) {
    const t = parts[i].trim();
    if (!t.startsWith('//')) break;
    lines.unshift(t.slice(2).trim());
  }
  return {
    summary: lines[0] || '',
    description: lines.join(' '),
  };
}

// ---------------------------------------------------------------------------
// Handler body analysis
// ---------------------------------------------------------------------------

function parseStatusCode(tok) {
  const t = (tok || '').trim();
  if (/^[0-9]+$/.test(t)) return parseInt(t, 10);
  const name = t.replace(/^http\./, '');
  if (HTTP_STATUS[name]) return HTTP_STATUS[name];
  return null;
}

function findRequestBody(body, structs) {
  const bm = /\bc\.ShouldBindJSON\s*\(\s*&(\w+)\s*\)/.exec(body);
  if (!bm) return null;
  const varName = bm[1];
  const esc = varName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const vm = new RegExp(`\\bvar\\s+${esc}\\s+([A-Z]\\w*Request\\w*)\\b`).exec(body);
  if (vm && structs[vm[1]]) return vm[1];
  const am = new RegExp(`\\b${esc}\\s*:?=\\s*(?:&)?([A-Z]\\w*Request\\w*)\\s*\\{`).exec(body);
  if (am && structs[am[1]]) return am[1];
  return null;
}

function resolveTypeName(raw, structs) {
  let t = (raw || '').trim();
  let array = false;
  while (t.startsWith('[]')) {
    array = true;
    t = t.slice(2).trim();
  }
  t = t.replace(/^\*/, '').trim();
  if (t === 'time.Time') return { array, name: 'date-time', format: 'date-time', struct: false };
  const core = t.split('.').pop();
  if (SCALAR.has(core)) return { array, name: core, struct: false };
  if (structs[core]) return { array, name: core, struct: true };
  return { array, name: core, struct: false };
}

function findVarType(body, name) {
  const esc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  let m = new RegExp(`\\bvar\\s+${esc}\\s+(\\[[^\\]]*\\])?\\s*([A-Za-z][\\w\\[\\]\\.\\*]*)\\s*(?:=|$)`).exec(body);
  if (m) return (m[1] || '') + (m[2] || '');
  m = new RegExp(`\\b${esc}\\s*:?=\\s*make\\((\\[[^\\]]*\\])?\\s*([A-Za-z][\\w\\[\\]\\.\\*]*)\\s*,`).exec(body);
  if (m) return (m[1] || '') + (m[2] || '');
  m = new RegExp(`\\b${esc}\\s*:?=\\s*(?:&)?([A-Z]\\w*)\\s*\\{`).exec(body);
  if (m) return m[1];
  return null;
}

function funcReturnType(source, fnName) {
  const re = new RegExp(`\\bfunc\\s+${fnName}\\s*\\([^)]*\\)\\s+([A-Za-z\\[\\]\\*\\.\\w]+)`);
  const m = re.exec(source);
  return m ? m[1] : null;
}

function resolveDataType(body, expr, source, structs) {
  const e = (expr || '').trim();
  if (!e || e === 'nil') return null;
  const fm = /^([A-Z]\w*)\(/.exec(e);
  if (fm) {
    const ret = funcReturnType(source, fm[1]);
    if (ret) return resolveTypeName(ret, structs);
    return null;
  }
  const lm = /^&?([A-Z]\w*)\s*\{/.exec(e);
  if (lm) return resolveTypeName(lm[1], structs);
  if (/^[a-zA-Z_]\w*$/.test(e)) {
    const vt = findVarType(body, e);
    if (vt) return resolveTypeName(vt, structs);
  }
  return null;
}

function findSuccessResponse(body, source, structs) {
  const wsRe = /httputil\.WriteSuccess(?:\[([^\]]+)\])?\s*\(/g;
  let m;
  let best = null;
  while ((m = wsRe.exec(body))) {
    const openIdx = body.indexOf('(', m.index);
    const close = findMatchingParen(body, openIdx);
    if (close < 0) continue;
    const args = splitTopLevel(body.slice(openIdx + 1, close), ',');
    if (args.length < 4) continue;
    const status = parseStatusCode(args[1]);
    const dataExpr = args.slice(3).join(',');
    const generic = (m[1] || '').trim();
    let type = null;
    if (generic === 'any' || generic === 'T') {
      type = resolveDataType(body, dataExpr, source, structs);
    } else if (generic) {
      type = resolveTypeName(generic, structs);
    } else {
      type = resolveDataType(body, dataExpr, source, structs);
    }
    if (status && (!best || status >= best.code)) {
      best = { code: status, type, raw: false };
    }
  }
  if (best) return best;
  if (/\bc\.Data\(\s*http\.StatusOK|json\.Marshal/.test(body)) {
    return { code: 200, type: { array: false, name: 'object', struct: false }, raw: true };
  }
  return null;
}

function findErrorCodes(body) {
  const codes = new Set();
  const we = /\bhttputil\.WriteError\s*\(\s*c\s*,\s*(http\.[A-Za-z]+|[0-9]+)\s*,/g;
  let m;
  while ((m = we.exec(body))) {
    const c = parseStatusCode(m[1]);
    if (c) codes.add(c);
  }
  if (/\bc\.ShouldBindJSON\b|httputil\.WriteValidationError\b/.test(body)) codes.add(400);
  return [...codes].sort((a, b) => a - b);
}

function collectPathParams(body, ginPath) {
  const fromPath = [...ginPath.matchAll(/:([^/]+)/g)].map((x) => x[1]);
  const fromHandler = [...body.matchAll(/\bc\.Param\(\s*"([^"]+)"\s*\)/g)].map((x) => x[1]);
  return [...new Set([...fromPath, ...fromHandler])];
}

function collectQueryParams(body) {
  const out = [];
  const re = /\bc\.(Query|QueryArray|QueryMap|DefaultQuery)\(\s*"([^"]+)"(\s*,\s*"([^"]*)")?\s*\)/g;
  let m;
  while ((m = re.exec(body))) {
    out.push({
      name: m[2],
      array: m[1] === 'QueryArray',
      map: m[1] === 'QueryMap',
      default: m[1] === 'DefaultQuery' ? m[4] : undefined,
    });
  }
  return out;
}

function ginToOpenApiPath(ginPath) {
  return ginPath
    .replace(/:([^/]+)/g, '{$1}')
    .replace(/\*([^/]+)/g, '{$1}');
}

// ---------------------------------------------------------------------------
// Struct -> JSON Schema
// ---------------------------------------------------------------------------

function parseStructFields(body) {
  const fields = [];
  for (const line of body.split('\n')) {
    const m = /^\s*([A-Z][A-Za-z0-9_]*)\s+(\S+)\s*(?:`([^`]*)`)?\s*(?:\/\/.*)?$/.exec(line);
    if (!m) continue;
    const name = m[1];
    const type = m[2];
    const tag = m[3] || '';
    const json = /json:"([^"]*)"/.exec(tag);
    let jsonName = null;
    let skip = false;
    if (json) {
      const first = json[1].split(',')[0];
      if (first === '-') skip = true;
      else jsonName = first;
    } else {
      jsonName = snakeCase(name);
    }
    const bindingMatch = /binding:"([^"]*)"/.exec(tag);
    const binding = bindingMatch ? bindingMatch[1] : '';
    fields.push({
      name,
      type,
      jsonName,
      skip,
      required: /\brequired\b/.test(binding),
      binding,
    });
  }
  return fields;
}

function scalarSchema(name) {
  switch (name) {
    case 'int':
    case 'int8':
    case 'int16':
    case 'int32':
    case 'uint8':
    case 'uint16':
    case 'uint32':
      return { type: 'integer', format: 'int32' };
    case 'int64':
    case 'uint64':
      return { type: 'integer', format: 'int64' };
    case 'float32':
      return { type: 'number', format: 'float' };
    case 'float64':
      return { type: 'number', format: 'double' };
    case 'bool':
      return { type: 'boolean' };
    case 'any':
    case 'object':
    case 'interface{}':
      return {};
    default:
      return { type: 'string' };
  }
}

function applyBinding(schema, binding) {
  if (!binding) return;
  if (schema.type === 'array' || schema.$ref) return;
  for (const part of binding.split(',')) {
    if (/^(required|omitempty|email|oneof|gt|lt|gte|lte|min|max)$/.test(part)) {
      if (part === 'email' && schema.type === 'string') schema.format = 'email';
      continue;
    }
    const idx = part.indexOf('=');
    if (idx < 0) continue;
    const k = part.slice(0, idx);
    const v = part.slice(idx + 1);
    if (k === 'oneof') schema.enum = v.split(' ').filter(Boolean);
    else if (k === 'min') {
      if (schema.type === 'string') schema.minLength = parseInt(v, 10);
      else schema.minimum = parseFloat(v);
    } else if (k === 'max') {
      if (schema.type === 'string') schema.maxLength = parseInt(v, 10);
      else schema.maximum = parseFloat(v);
    } else if (k === 'gt') schema.exclusiveMinimum = parseFloat(v);
    else if (k === 'lt') schema.exclusiveMaximum = parseFloat(v);
  }
}

function goTypeToSchema(raw, structs, components, memo, prefix, binding) {
  let t = (raw || '').trim();
  let array = false;
  while (t.startsWith('[]')) {
    array = true;
    t = t.slice(2).trim();
  }
  const pointer = t.startsWith('*');
  if (pointer) t = t.slice(1).trim();

  let schema;
  if (t === 'time.Time') schema = { type: 'string', format: 'date-time' };
  else if (t === '[]byte') schema = { type: 'string', format: 'byte' };
  else if (/^map\[/.test(t)) schema = { type: 'object' };
  else if (SCALAR.has(t.split('.').pop())) schema = scalarSchema(t.split('.').pop());
  else if (structs[t.split('.').pop()]) {
    const core = t.split('.').pop();
    const refName = (prefix ? prefix + '_' : '') + core;
    ensureStructSchema(core, refName, structs, components, memo, prefix);
    schema = { $ref: `#/components/schemas/${refName}` };
  } else schema = { type: 'object' };

  if (array) schema = { type: 'array', items: schema };
  if (pointer && schema.type) schema.nullable = true;
  applyBinding(schema, binding);
  return schema;
}

function ensureStructSchema(name, refName, structs, components, memo, prefix) {
  memo = memo || new Set();
  if (memo.has(refName)) return;
  const st = structs[name];
  if (!st) return;
  memo.add(refName);
  const props = {};
  const required = [];
  for (const f of parseStructFields(st.body)) {
    if (!f.jsonName || f.skip) continue;
    props[f.jsonName] = goTypeToSchema(f.type, structs, components, memo, prefix, f.binding);
    if (f.required) required.push(f.jsonName);
  }
  const schema = { type: 'object', properties: props };
  if (required.length) schema.required = required;
  components.schemas[refName] = schema;
}

// ---------------------------------------------------------------------------
// Operation building
// ---------------------------------------------------------------------------

function envelopeSchema(typeNode, prefix, structs, components) {
  const schema = {
    type: 'object',
    properties: {
      status: { type: 'string', example: 'success' },
      message: { type: 'string' },
    },
    required: ['status'],
  };
  if (typeNode) {
    schema.properties.data = typeToSchema(typeNode, prefix, structs, components);
    schema.required.push('data');
  }
  return schema;
}

function typeToSchema(t, prefix, structs, components) {
  if (!t) return {};
  if (t.struct) {
    const refName = (prefix ? prefix + '_' : '') + t.name;
    ensureStructSchema(t.name, refName, structs, components, null, prefix);
    const ref = { $ref: `#/components/schemas/${refName}` };
    return t.array ? { type: 'array', items: ref } : ref;
  }
  if (t.format) {
    const s = { type: 'string', format: t.format };
    return t.array ? { type: 'array', items: s } : s;
  }
  if (SCALAR.has(t.name)) {
    const s = scalarSchema(t.name);
    return t.array ? { type: 'array', items: s } : s;
  }
  return t.array ? { type: 'array', items: { type: 'object' } } : { type: 'object' };
}

function buildOperation(op, structs, prefix, ctx) {
  const operation = {
    tags: [op.service.name],
    summary: op.summary,
    operationId: op.handlerMethod
      ? (prefix ? prefix + '_' : '') + op.handlerMethod
      : (prefix ? prefix + '_' : '') + 'health',
  };
  if (op.description) operation.description = op.description;

  const secName = op.security.internal ? 'internal' : op.security.jwt ? 'bearer' : null;
  if (secName) {
    ctx.used.add(secName);
    operation.security = [
      { [secName === 'bearer' ? 'bearerAuth' : 'internalToken']: [] },
    ];
  }
  if (op.security.perms.length) {
    operation['x-required-permission'] =
      op.security.perms.length === 1 ? op.security.perms[0] : op.security.perms;
  }
  operation['x-category'] = op.security.category;
  operation['x-service'] = op.service.name;
  if (op.xSource) operation['x-source'] = op.xSource;

  const params = [];
  for (const p of op.pathParams) {
    params.push({
      name: p,
      in: 'path',
      required: true,
      schema: { type: 'string' },
      description: `Path parameter \`${p}\``,
    });
  }
  for (const q of op.queryParams) {
    const schema = q.array
      ? { type: 'array', items: { type: 'string' } }
      : q.map
        ? { type: 'object' }
        : { type: 'string' };
    if (q.default !== undefined) schema.default = q.default;
    params.push({ name: q.name, in: 'query', required: false, schema });
  }
  if (params.length) operation.parameters = params;

  if (op.requestStruct) {
    const refName = (prefix ? prefix + '_' : '') + op.requestStruct;
    ensureStructSchema(op.requestStruct, refName, structs, ctx.components, null, prefix);
    operation.requestBody = {
      required: true,
      content: {
        'application/json': {
          schema: { $ref: `#/components/schemas/${refName}` },
        },
      },
    };
  }

  const responses = {};
  const success = op.success;
  if (success && success.code) {
    responses[success.code] = {
      description: SUCCESS_DESCRIPTIONS[success.code] || 'Successful response',
      content: {
        'application/json': {
          schema: success.raw
            ? { type: 'object' }
            : envelopeSchema(success.type, prefix, structs, ctx.components),
        },
      },
    };
  } else {
    responses[200] = {
      description: 'Successful response',
      content: {
        'application/json': {
          schema: envelopeSchema(null, prefix, structs, ctx.components),
        },
      },
    };
  }

  const codes = new Set(op.errorCodes || []);
  if (op.security.jwt) codes.add(401);
  if (op.security.internal) codes.add(401);
  if (op.requestStruct) codes.add(400);
  for (const c of [...codes].sort((a, b) => a - b)) {
    if (responses[c]) continue;
    responses[c] = {
      description: ERROR_DESCRIPTIONS[c] || 'Error',
      content: {
        'application/json': {
          schema: { $ref: '#/components/schemas/ErrorResponse' },
        },
      },
    };
  }
  operation.responses = responses;
  return operation;
}

// ---------------------------------------------------------------------------
// Spec assembly
// ---------------------------------------------------------------------------

function buildSecuritySchemes(used) {
  const schemes = {};
  if (used.has('bearer')) {
    schemes.bearerAuth = { type: 'http', scheme: 'bearer', bearerFormat: 'JWT' };
  }
  if (used.has('internal')) {
    schemes.internalToken = { type: 'apiKey', in: 'header', name: 'X-Internal-Service-Token' };
  }
  return schemes;
}

function generatedDescription() {
  return `Auto-generated by \`node scripts/generate-openapi.js\` from the Gin routers and handler DTOs. Do not edit by hand.`;
}

function buildPerServiceDoc(svc, ops, structs, opts) {
  const ctx = { components: { schemas: {} }, used: new Set() };
  const paths = {};
  for (const op of ops) {
    const p = paths[op.path] || (paths[op.path] = {});
    p[op.method.toLowerCase()] = buildOperation(op, structs, '', ctx);
  }
  ctx.components.schemas.ErrorResponse = {
    type: 'object',
    required: ['error'],
    properties: { error: { type: 'string' } },
  };
  return {
    openapi: '3.0.1',
    info: {
      title: `${svc.title} API`,
      description: generatedDescription(),
      version: '1.0.0',
    },
    servers: [{ url: opts.server || defaultServer(svc) }],
    tags: [{ name: svc.name, description: svc.title }],
    paths,
    components: {
      securitySchemes: buildSecuritySchemes(ctx.used),
      schemas: ctx.components.schemas,
    },
  };
}

function buildCombinedDoc(groups, opts) {
  const ctx = { components: { schemas: {} }, used: new Set() };
  const paths = {};
  const tags = [];
  const servers = [];
  for (const { svc, ops, structs } of groups) {
    tags.push({ name: svc.name, description: svc.title });
    servers.push({
      url: opts.server || defaultServer(svc),
      description: svc.title,
    });
    const prefix = shortName(svc);
    for (const op of ops) {
      const key = op.method.toLowerCase();
      const existing = paths[op.path] && paths[op.path][key];
      if (existing) {
        if (!existing.tags.includes(svc.name)) existing.tags.push(svc.name);
        console.warn(
          `[warn] duplicate ${op.method} ${op.path} from ${svc.name} — merged into existing operation`
        );
        continue;
      }
      const p = paths[op.path] || (paths[op.path] = {});
      p[key] = buildOperation(op, structs, prefix, ctx);
    }
  }
  ctx.components.schemas.ErrorResponse = {
    type: 'object',
    required: ['error'],
    properties: { error: { type: 'string' } },
  };
  return {
    openapi: '3.0.1',
    info: {
      title: 'Microservice API',
      description:
        'Combined OpenAPI specification for the microservice fleet. ' +
        generatedDescription(),
      version: '1.0.0',
    },
    servers,
    tags,
    paths,
    components: {
      securitySchemes: buildSecuritySchemes(ctx.used),
      schemas: ctx.components.schemas,
    },
  };
}

// ---------------------------------------------------------------------------
// Minimal YAML emitter
// ---------------------------------------------------------------------------

function yamlStr(v) {
  if (typeof v !== 'string') return String(v);
  if (
    /^[A-Za-z_][A-Za-z0-9_.\- ]*$/.test(v) &&
    !/^(true|false|null|yes|no|on|off)$/i.test(v) &&
    !/^-?[0-9][0-9.]*$/.test(v) &&
    !/^0x[0-9a-f]+$/i.test(v)
  ) {
    return v;
  }
  return JSON.stringify(v);
}

function emitPair(k, v, indent) {
  const pad = ' '.repeat(indent);
  const key = yamlStr(k);
  if (v === null || v === undefined) return [`${pad}${key}: null`];
  if (Array.isArray(v)) {
    if (v.length === 0) return [`${pad}${key}: []`];
    const out = [`${pad}${key}:`];
    for (const item of v) {
      if (typeof item === 'object' && item !== null) {
        out.push(`${pad}  -`);
        out.push(...emitObject(item, indent + 4));
      } else {
        out.push(`${pad}  - ${yamlStr(item)}`);
      }
    }
    return out;
  }
  if (typeof v === 'object') {
    if (Object.keys(v).length === 0) return [`${pad}${key}: {}`];
    const out = [`${pad}${key}:`];
    out.push(...emitObject(v, indent + 2));
    return out;
  }
  if (typeof v === 'boolean' || typeof v === 'number') return [`${pad}${key}: ${v}`];
  return [`${pad}${key}: ${yamlStr(v)}`];
}

function emitObject(obj, indent) {
  const lines = [];
  for (const [k, v] of Object.entries(obj)) {
    lines.push(...emitPair(k, v, indent));
  }
  return lines;
}

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------

function writeDoc(filePath, spec) {
  const abs = path.resolve(REPO, filePath);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  const body = opts.format === 'json' ? JSON.stringify(spec, null, 2) : emitObject(spec, 0).join('\n');
  fs.writeFileSync(abs, body + '\n');
  return abs;
}

function printSummary(groups, written) {
  console.log(`\nGenerated OpenAPI specs (${opts.format}):`);
  let total = 0;
  for (const [rel, ops] of written) {
    console.log(`  ${rel}  (${ops} operations)`);
    total += ops;
  }
  console.log(`\nTotal: ${total} operations across ${groups.length} services.`);
  for (const { svc, ops } of groups) {
    console.log(`\n${svc.name}:`);
    for (const op of ops) {
      const auth =
        op.security.category === 'internal'
          ? 'internal'
          : op.security.category === 'authenticated'
            ? 'jwt' + (op.security.perms.length ? `:${op.security.perms.join(',')}` : '')
            : 'public';
      console.log(
        `  ${op.method.padEnd(6)} ${op.path.padEnd(38)} ${(op.handlerMethod || 'health').padEnd(26)} ${auth}`
      );
    }
  }
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

function scanService(svc) {
  const routerFile = path.join(REPO, svc.name, 'cmd', 'router.go');
  if (!fs.existsSync(routerFile)) return null;
  const routerSrc = fs.readFileSync(routerFile, 'utf8');
  const { byMethod, structs } = indexHandlers(path.join(REPO, svc.name));
  const { groups, routes } = parseRouter(routerSrc);

  const ops = [];
  for (const route of routes) {
    const security = routeSecurity(route, groups);
    const handlerRef = handlerRefFromRoute(route.args);
    const group = groups[route.varName] || { prefix: '' };
    const fullGinPath = group.prefix + route.ginPath;
    const op = {
      service: svc,
      method: route.method,
      ginPath: fullGinPath,
      path: ginToOpenApiPath(fullGinPath),
      security,
      pathParams: [],
      queryParams: [],
      requestStruct: null,
      success: null,
      errorCodes: [],
      summary: '',
      description: '',
      handlerMethod: '',
      xSource: [`${svc.name}/cmd/router.go:${route.line}`],
    };

    if (handlerRef && handlerRef.inline) {
      op.summary = 'Health check';
      op.description = 'Liveness probe returning HTTP 200 when the service is up.';
      op.success = { code: 200, type: null, raw: false };
      ops.push(op);
      continue;
    }

    const method = handlerRef ? handlerRef.method : '';
    op.handlerMethod = method;
    const h = byMethod[method];
    if (!h) {
      op.summary = method ? humanize(method) : 'Endpoint';
      op.success = { code: 200, type: null, raw: false };
      op.warning = `handler ${method} not found`;
      ops.push(op);
      continue;
    }

    op.summary = h.comment.summary || humanize(method);
    op.description = h.comment.description || '';
    op.pathParams = collectPathParams(h.body, fullGinPath);
    op.queryParams = collectQueryParams(h.body);
    op.requestStruct = findRequestBody(h.body, structs);
    const success = findSuccessResponse(h.body, h.source, structs);
    op.success = success || { code: 200, type: null, raw: false };
    op.errorCodes = findErrorCodes(h.body);
    op.xSource.push(`${relOf(h.file)}:${h.line}`);
    ops.push(op);
  }

  return { svc, ops, structs };
}

function main() {
  const services = SERVICES.filter((s) => opts.filter.includes(s.name));
  const groups = [];
  for (const svc of services) {
    const scanned = scanService(svc);
    if (!scanned) {
      console.log(`[skip] ${svc.name}: no cmd/router.go (worker, not HTTP)`);
      continue;
    }
    groups.push(scanned);
  }

  if (!groups.length) {
    console.error('No services with an HTTP router were found.');
    process.exit(1);
  }

  const written = [];
  for (const { svc, ops } of groups) {
    if (opts.perService) {
      const spec = buildPerServiceDoc(svc, ops, groups.find((g) => g.svc === svc).structs, opts);
      const rel = path.join(opts.out, `${svc.name}.${opts.format === 'json' ? 'json' : 'yaml'}`);
      writeDoc(rel, spec);
      written.push([rel, ops.length]);
    }
  }
  if (opts.combined) {
    const spec = buildCombinedDoc(groups, opts);
    const rel = path.join(opts.out, `openapi.${opts.format === 'json' ? 'json' : 'yaml'}`);
    writeDoc(rel, spec);
    const total = groups.reduce((n, g) => n + g.ops.length, 0);
    written.push([rel, total]);
  }

  printSummary(groups, written);
}

main();
