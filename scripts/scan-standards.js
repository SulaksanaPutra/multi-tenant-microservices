#!/usr/bin/env node
/**
 * scan-standards.js — CODING_STANDARDS.md compliance scanner
 * ==========================================================
 *
 * A single-file, zero-dependency regex scanner that flags common violations of
 * the repository's CODING_STANDARDS.md across all Go microservices.
 *
 * Usage:
 *   node scripts/scan-standards.js                    # scan every service
 *   node scripts/scan-standards.js --service auth     # one service
 *   node scripts/scan-standards.js --json             # machine-readable output
 *   node scripts/scan-standards.js --quiet            # summary only
 *   node scripts/scan-standards.js --help
 *
 * Exit code:
 *   0  no violations found
 *   1  one or more violations were found (CI-friendly)
 *
 * Scope / limitations (see CODING_STANDARDS.md):
 *   These checks cover the written-style and structural rules that are
 *   robustly expressible as regular expressions. Dependency direction (Rule 2.2)
 *   is approximated at the import/interface boundary for Layer 1 files
 *   (consumer/handler): they must not import Layer 3 (repository/publisher) or
 *   declare repository-named interfaces. The following CANNOT be detected by a
 *   regex tool and are intentionally out of scope (they require Go AST /
 *   nesting analysis):
 *     - Rule 5.4 external I/O inside transaction closures (nesting analysis)
 *     - Rule 4.3 exact "<package>: <action>" prefix per call site
 *     - bare single-letter identifiers `r` / `h` (too many false positives:
 *       routers, range variables, rune literals, etc.)
 */

'use strict';

const fs = require('fs');
const path = require('path');

const REPO = path.resolve(__dirname, '..');
const SERVICES = [
  'auth-service',
  'order-service',
  'tenant-service',
  'notification-service',
  'user-service',
  'infra-provisioner',
];

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------
const args = process.argv.slice(2);
const opts = { json: false, quiet: false, filter: SERVICES, strict: false };
for (let i = 0; i < args.length; i++) {
  switch (args[i]) {
    case '--json':
      opts.json = true;
      break;
    case '--quiet':
      opts.quiet = true;
      break;
    case '--service':
      opts.filter = [args[++i]];
      break;
    case '--strict':
      opts.strict = true;
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
  console.log(`CODING_STANDARDS.md compliance scanner

Usage:
  node ${p}                     scan every service (non-test code)
  node ${p} --strict            include *_test.go in style checks
  node ${p} --service user      scan one service
  node ${p} --json              machine-readable output
  node ${p} --quiet             summary only

Exit code 1 when violations are found.`);
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
/** Remove comment and string tokens from a single line (heuristic). */
function scrubLine(line) {
  return line
    .replace(/\/\*.*?\*\//g, ' ')
    .replace(/\/\/.*$/g, ' ')
    .replace(/`[^`]*`/g, ' ')
    .replace(/'[^']*'/g, ' ')
    .replace(/"(?:[^"\\]|\\.)*"/g, ' ');
}

/** Scrub an entire file, keeping line structure intact. */
function scrubContent(content) {
  return content.split('\n').map(scrubLine).join('\n');
}

function lineOf(content, index) {
  return content.slice(0, index).split('\n').length;
}

function listGoFiles(service) {
  const base = path.join(REPO, service);
  const out = [];
  if (!fs.existsSync(base)) return out;
  (function walk(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.isFile() && entry.name.endsWith('.go')) out.push(full);
    }
  })(base);
  return out;
}

function relOf(file) {
  return file.slice(REPO.length + 1).replace(/\\/g, '/');
}

// ---------------------------------------------------------------------------
// Check registry
// ---------------------------------------------------------------------------
const CHECKS = [];

/** Register a regex-based check. scope/exclude are predicates on relPath. */
function addRegexCheck(cfg) {
  CHECKS.push({
    ...cfg,
    run(file, rel, content, service) {
      if (cfg.scope && !cfg.scope(rel)) return [];
      if (cfg.exclude && cfg.exclude(rel)) return [];
      if (cfg.onlyWhenStrict && !opts.strict && /_test\.go$/.test(rel)) return [];
      const source = cfg.scrub ? scrubContent(content) : content;
      const flags = (cfg.flags || '').includes('g') ? cfg.flags : (cfg.flags || '') + 'g';
      const re = new RegExp(cfg.regex.source, flags);
      const out = [];
      let m;
      while ((m = re.exec(source))) {
        out.push({
          service,
          rule: cfg.rule,
          id: cfg.id,
          path: rel,
          line: cfg.scrub ? source.slice(0, m.index).split('\n').length : lineOf(content, m.index),
          message: cfg.message(m),
        });
      }
      return out;
    },
  });
}

// Rule 2.1 — constructors return concrete struct pointers, not interfaces/values.
addRegexCheck({
  id: 'constructor-returns-concrete',
  rule: '2.1',
  name: 'New* constructors must return concrete struct pointers',
  regex: /\bfunc\s+(?:\([^)]*\)\s*)?New\w+\s*\([^)]*\)\s+([A-Z][A-Za-z0-9_]*)\b/,
  message: (m) =>
    `New* constructor returns non-pointer type \`${m[1]}\` — return a concrete *${m[1]} pointer`,
});

// Rule 3.1 — collection queries must be named List*, not Get*.
addRegexCheck({
  id: 'get-returns-collection',
  rule: '3.1',
  name: 'Collection methods named Get* should be List*',
  regex: /\bfunc\s+(?:\([^)]*\)\s*)?Get\w+\s*\([^)]*\)\s*(\[\])/,
  message: () =>
    'method returning a slice is named `Get*` — rule 3.1 requires `List*` for collection queries',
});

// Rule 3.4 — full-word layer variable naming (bare r/h excluded: ambiguous).
// These are opt-in for test files via --strict; default scope is production code.
addRegexCheck({
  id: 'abbrev-repo',
  rule: '3.4',
  name: 'No abbreviated repository identifiers',
  scrub: true,
  onlyWhenStrict: true,
  regex: /\b(?:\w*Repo\b|\brepo\b)/,
  message: () =>
    'abbreviated repository identifier (`repo` / `*Repo`) — use a full word like `roleRepository`',
});
addRegexCheck({
  id: 'abbrev-svc',
  rule: '3.4',
  name: 'No abbreviated service identifiers',
  scrub: true,
  onlyWhenStrict: true,
  regex: /\b\w*[sS]vc\b/,
  message: () =>
    'abbreviated service identifier (`svc` / `*Svc`) — use a full word like `workspaceService`',
});
addRegexCheck({
  id: 'abbrev-pub',
  rule: '3.4',
  name: 'No abbreviated publisher identifiers',
  scrub: true,
  onlyWhenStrict: true,
  scope: (rel) => /\/(consumer|publisher|worker)\//.test(rel),
  regex: /\bpub\b/,
  message: () =>
    'abbreviated publisher identifier (`pub`) — use a full word like `tenantEventPublisher`',
});
addRegexCheck({
  id: 'abbrev-cons',
  rule: '3.4',
  name: 'No abbreviated consumer identifiers',
  scrub: true,
  onlyWhenStrict: true,
  regex: /\b\w*[cC]ons\b/,
  message: () =>
    'abbreviated consumer identifier (`cons` / `*Cons`) — use a full word like `userCreatedConsumer`',
});
addRegexCheck({
  id: 'abbrev-hnd',
  rule: '3.4',
  name: 'No abbreviated handler identifiers',
  scrub: true,
  onlyWhenStrict: true,
  regex: /\b\w*[hH]nd\b/,
  message: () =>
    'abbreviated handler identifier (`hnd` / `*Hnd`) — use a full word like `orderHandler`',
});
addRegexCheck({
  id: 'abbrev-mig',
  rule: '3.4',
  name: 'No abbreviated migration identifiers',
  scrub: true,
  onlyWhenStrict: true,
  regex: /\bmig\b/,
  message: () => 'abbreviated migration identifier (`mig`) — use `migration`',
});

// Rule 4.1 — exported sentinels must live in internal/domain only.
addRegexCheck({
  id: 'sentinel-outside-domain',
  rule: '4.1',
  name: 'Sentinel errors must be declared in internal/domain',
  exclude: (rel) => /\/domain\//.test(rel),
  regex: /^\s*(?:var\s+)?(?:[A-Za-z0-9_]+\s+)?(Err\w+)\s*=\s*(?:errors\.New|fmt\.Errorf)\(/m,
  flags: 'm',
  message: (m) =>
    `sentinel \`${m[1]}\` declared outside internal/domain — export it from the domain package`,
});

// Rule 4.3 — fmt.Errorf with no format verb (unwrapped / not wrapping).
addRegexCheck({
  id: 'fmt-errorf-static',
  rule: '4.3',
  name: 'Errors should follow <pkg>: <action>: %w wrapping format',
  regex: /fmt\.Errorf\("([^"]*)"\)/,
  message: (m) =>
    `fmt.Errorf("${m[1]}") has no format verb and no %w — prefer errors.New / a domain sentinel or add %w`,
});

// Rule 4.4 — database/sql leaked into the service layer.
addRegexCheck({
  id: 'sql-in-service',
  rule: '4.4',
  name: 'Services must not import database/sql or check sql.ErrNoRows',
  scope: (rel) => /\/service\//.test(rel),
  exclude: (rel) => /order-service\/internal\/service\/migration_service/.test(rel),
  regex: /"database\/sql"|\bsql\.(ErrNoRows|DB|Tx|Open|Query|Exec|Stmt|Std|Scan)\b/,
  message: () =>
    '`database/sql`/`sql.*` used in the service layer — repositories must translate driver errors to domain sentinels',
});

// Rule 2.2 — Layer 1 (consumer/handler) must depend on Layer 2 (service), not Layer 3.
// An import-graph approximation: a Layer 1 file that imports its own service's
// internal/repository or internal/publisher skips the service layer. Worker outbox
// polling is a documented exemption (Flow C), so scope is consumer/ and handler/ only.
addRegexCheck({
  id: 'layer-imports-repository',
  rule: '2.2',
  name: 'Layer 1 (consumer/handler) must not import Layer 3 (repository/publisher)',
  scope: (rel) => /\/(consumer|handler)\//.test(rel),
  exclude: (rel) => /_test\.go$/.test(rel),
  regex: /internal\/(repository|publisher)"\s*$/m,
  flags: 'm',
  message: () =>
    'imports the service\'s own `internal/repository`/`internal/publisher` from Layer 1 — depend on Layer 2 (service) via a consumer-side interface instead',
});

// Rule 2.2 — Layer 1 consumer-side interfaces are service-shaped, never repository-named.
// Catches the pattern where a consumer declares an abstraction over a concrete
// repository (e.g. `type MembershipRepository interface`) even when the file does
// not literally import the repository package.
addRegexCheck({
  id: 'layer-interface-repository-name',
  rule: '2.2',
  name: 'Layer 1 consumer-side interfaces must not be repository-named',
  scope: (rel) => /\/(consumer|handler)\//.test(rel),
  exclude: (rel) => /_test\.go$/.test(rel),
  regex: /^\s*type\s+(\w*Repository)\s+interface\b/m,
  flags: 'm',
  message: (m) =>
    `Layer 1 declares an interface named \`${m[1]}\` — consumer-side interfaces should be service-shaped (e.g. MembershipService), exposing business operations not repository methods`,
});

// Rule 5.1 — AMQP topology constants must live in domain/events.go.
addRegexCheck({
  id: 'amqp-const-outside-domain',
  rule: '5.1',
  name: 'AMQP topology constants must be centralized in domain/events.go',
  scope: (rel) => /\/(consumer|publisher)\//.test(rel),
  regex: /^\s*(\w*(?:Exchange|RoutingKey|Queue)\w*)\s*=\s*"/m,
  flags: 'm',
  message: (m) =>
    `AMQP constant \`${m[1]}\` declared in consumer/publisher — centralize in domain/events.go`,
});

// Rule 6.1 — repository write methods must not accept raw domain entities.
addRegexCheck({
  id: 'repo-write-accepts-domain',
  rule: '6.1',
  name: 'Repository write methods must accept {Action}{Entity}Input DTOs',
  regex: /\bfunc\s+\([^)]*\)\s+(?:Bulk)?(?:Create|Update|Upsert|Save|Insert|Delete)\w*\s*\([^)]*domain\.\w+/,
  message: () =>
    'repo write method accepts a raw `domain.*` struct — define an `{Action}{Entity}Input` DTO instead',
});

// Rule 6.1 — service methods must return {UseCase}Output DTOs, not raw domain entities.
// Matches service methods whose (first) return type references a `domain.*` entity
// directly (value, pointer, or slice). Consumer-side repository interfaces in the
// service package are `type ... interface` declarations, not `func`s, so they are
// correctly exempt — repositories legitimately return domain entities.
addRegexCheck({
  id: 'service-returns-domain',
  rule: '6.1',
  name: 'Service methods must return {UseCase}Output DTOs, not domain entities',
  scope: (rel) => /\/service\//.test(rel),
  exclude: (rel) =>
    /_test\.go$/.test(rel) ||
    /order-service\/internal\/service\/migration_service/.test(rel) ||
    // InboxService.GetBarrierEvents is a Layer-1 infrastructure accessor (event
    // inbox barrier read). By design it returns the raw []domain.InboxMessage rows
    // as data fed into the business service (see Rule 7.2 / event inbox pattern),
    // not a handler-facing use-case Output. Exempting it mirrors migration_service.
    /notification-service\/internal\/service\/inbox_service/.test(rel),
  regex: /\bfunc\s+(?:\([^)]*\)\s*)?[A-Z]\w*\s*\([^)]*\)\s*\(?\s*(?:\[\s*\]|\*)?\s*domain\.\w+/,
  message: () =>
    'service method returns a raw `domain.*` entity — expose a `{UseCase}Output` DTO instead and map it in the handler layer',
});

// Rule 6.1 — service DTOs must not carry JSON tags.
addRegexCheck({
  id: 'json-tag-in-service',
  rule: '6.1',
  name: 'Service DTOs must be transport-agnostic (no json tags)',
  scope: (rel) => /\/service\//.test(rel),
  exclude: (rel) => /_test\.go$/.test(rel),
  regex: /json:"/,
  message: () => '`json:"..."` tag found in the service layer — JSON belongs in handler DTOs only',
});

// Rule 6.2 — domain entities must not be suffixed with Record.
addRegexCheck({
  id: 'record-suffix-domain',
  rule: '6.2',
  name: 'Domain entities must not be suffixed with Record',
  scope: (rel) => /\/domain\//.test(rel),
  regex: /\btype\s+(\w+Record)\b/,
  message: (m) => `domain entity \`type ${m[1]}\` uses a prohibited \`Record\` suffix`,
});

// Rule 6.3 — internal_* file must carry an Internal* struct, and vice-versa.
function isInternalFile(rel) {
  return (
    /internal_[a-z0-9_]+\.go$/.test(rel) &&
    /_(handler|service)\.go$/.test(rel) &&
    /_test\.go$/.test(rel) === false
  );
}

CHECKS.push({
  id: 'internal-file-without-internal-type',
  rule: '6.3',
  name: 'internal_* files must declare an Internal* handler/service',
  run: (file, rel, content, service) => {
    if (!isInternalFile(rel)) return [];
    if (/\btype\s+Internal\w+/.test(content)) return [];
    return [
      {
        service,
        rule: '6.3',
        id: 'internal-file-without-internal-type',
        path: rel,
        line: 1,
        message: `file \`${rel}\` lacks a matching \`type Internal...\` — the struct must be Internal-prefixed per Rule 6.3`,
      },
    ];
  },
});

CHECKS.push({
  id: 'internal-type-without-internal-file',
  rule: '6.3',
  name: 'Internal* handlers/services must live in internal_* files',
  run: (file, rel, content, service) => {
    if (isInternalFile(rel)) return [];
    if (!/\/(handler|service)\//.test(rel)) return [];
    const re = /\btype\s+(Internal\w+(?:Handler|Service|Resolver)\b)/g;
    const out = [];
    let m;
    while ((m = re.exec(content))) {
      out.push({
        service,
        rule: '6.3',
        id: 'internal-type-without-internal-file',
        path: rel,
        line: lineOf(content, m.index),
        message: `\`type ${m[1]}\` is not in an \`internal_*_handler|service.go\` file — internal structs need the internal_ file prefix per Rule 6.3`,
      });
    }
    return out;
  },
});

// Rule 7.1 — inline DDL in Go source.
addRegexCheck({
  id: 'inline-ddl',
  rule: '7.1',
  name: 'Schema DDL must live in migrations/, not inline in Go',
  exclude: (rel) =>
    /_test\.go$/.test(rel) ||
    /e2e-tests\//.test(rel) ||
    /order-service\/internal\/service\/migration_service/.test(rel),
  regex: /\bCREATE\s+(?:TABLE|INDEX|SEQUENCE|SCHEMA|VIEW|TYPE)\b/i,
  message: () => 'inline DDL (`CREATE ...`) in a Go source file — move to a versioned SQL migration',
});

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------
function collect() {
  const all = [];
  for (const service of opts.filter) {
    if (!SERVICES.includes(service)) continue;
    for (const file of listGoFiles(service)) {
      const rel = relOf(file);
      const content = fs.readFileSync(file, 'utf8');
      for (const c of CHECKS) all.push(...c.run(file, rel, content, service));
    }
  }
  return all;
}

// Rule 8.1 — every package must ship a *_test.go (filesystem check).
function collectTestCoverage() {
  const findings = [];
  for (const service of opts.filter) {
    if (!SERVICES.includes(service)) continue;
    const base = path.join(REPO, service);
    if (!fs.existsSync(base)) continue;
    (function walk(dir) {
      let hasCode = false;
      let hasTest = false;
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        if (entry.isDirectory()) {
          const child = path.join(dir, entry.name);
          const childState = walk(child);
          if (childState.hasCode) hasCode = true;
          if (childState.hasTest) hasTest = true;
        } else if (entry.name.endsWith('.go')) {
          if (entry.name.endsWith('_test.go')) hasTest = true;
          else hasCode = true;
        }
      }
      const state = { hasCode, hasTest };
      if (hasCode && !hasTest) {
        const rel = relOf(dir);
        if (/\/(testutil|internal\/testutil)$/.test(rel)) return state;
        if (/\/cmd$/.test(rel)) return state; // composition roots: no testable exported surface
        findings.push({
          service,
          rule: '8.1',
          id: 'package-without-tests',
          path: rel,
          line: 1,
          message: `package \`${rel}\` has non-test Go files but no \`*_test.go\``,
        });
      }
      return state;
    })(base);
  }
  return findings;
}

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------
function main() {
  const findings = [...collect(), ...collectTestCoverage()].sort(
    (a, b) => a.service.localeCompare(b.service) || a.path.localeCompare(b.path) || a.line - b.line
  );

  if (opts.json) {
    console.log(JSON.stringify(findings, null, 2));
  } else if (opts.quiet) {
    const byService = {};
    for (const f of findings) byService[f.service] = (byService[f.service] || 0) + 1;
    if (findings.length === 0) {
      console.log('No violations found. Clean!');
    } else {
      for (const [svc, n] of Object.entries(byService)) console.log(`${svc}: ${n}`);
    }
  } else if (findings.length === 0) {
    console.log('No violations found. Clean!');
  } else {
    let current = '';
    for (const f of findings) {
      if (f.service !== current) {
        current = f.service;
        console.log(`\n== ${current} ==`);
      }
      console.log(`  [Rule ${f.rule}] ${f.id}\n    ${f.path}:${f.line}\n    ${f.message}`);
    }
    console.log(
      `\nTotal: ${findings.length} violations across ${new Set(findings.map((f) => f.service)).size} services`
    );
  }

  process.exit(findings.length ? 1 : 0);
}

main();