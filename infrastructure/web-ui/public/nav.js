const CURRENT_SESSION_ID = "{{SERVER_SESSION_ID}}";
const STORAGE_KEY_TENANTS = "broker_demo_tenants";
const STORAGE_KEY_SESSION = "broker_demo_session_id";
const STORAGE_KEY_JWT = "broker_demo_jwt_token";
const STORAGE_KEY_REGISTERED = "broker_demo_registered";
const STORAGE_KEY_MAIL_CLICKED = "broker_demo_mail_clicked";
const STORAGE_KEY_PASSWORD_SET = "broker_demo_password_set";

function initSessionState() {
    const savedSession = localStorage.getItem(STORAGE_KEY_SESSION);
    if (savedSession !== CURRENT_SESSION_ID) {
        localStorage.clear();
        localStorage.setItem(STORAGE_KEY_SESSION, CURRENT_SESSION_ID);
        localStorage.setItem(STORAGE_KEY_TENANTS, JSON.stringify([]));
        console.log("Fresh server session detected. Local state purged cleanly.");
    }
}

function injectResponsiveStyles() {
    if (document.getElementById("responsive-style-tag")) return;
    const style = document.createElement("style");
    style.id = "responsive-style-tag";
    style.textContent = `
        * {
            box-sizing: border-box;
        }
        body {
            margin: 0 auto;
            padding: 16px;
            max-width: 1200px;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            color: #222;
            background-color: #fff;
            line-height: 1.5;
        }
        .table-responsive {
            width: 100%;
            overflow-x: auto;
            -webkit-overflow-scrolling: touch;
            margin-bottom: 15px;
        }
        pre {
            white-space: pre-wrap;
            word-break: break-all;
            max-width: 100%;
            overflow-x: auto;
        }
        @media (max-width: 768px) {
            body {
                padding: 10px;
            }
            h1 { font-size: 1.4rem; }
            h2 { font-size: 1.2rem; }
            h3 { font-size: 1.1rem; }
            nav {
                font-size: 0.9rem;
                line-height: 2 !important;
            }
            .responsive-split {
                flex-direction: column !important;
                width: 100% !important;
            }
            .responsive-split > div {
                width: 100% !important;
                min-width: 100% !important;
            }
            dialog {
                width: 94% !important;
                max-width: 94% !important;
                padding: 15px !important;
            }
            input[type="text"],
            input[type="email"],
            input[type="password"],
            input[type="number"],
            select {
                max-width: 100% !important;
            }
        }
    `;
    document.head.appendChild(style);
}

function isRegistered() {
    return localStorage.getItem(STORAGE_KEY_REGISTERED) === "true";
}

function isMailClicked() {
    return localStorage.getItem(STORAGE_KEY_MAIL_CLICKED) === "true";
}

function isPasswordSet() {
    return localStorage.getItem(STORAGE_KEY_PASSWORD_SET) === "true";
}

const STORAGE_KEY_SESSIONS = "broker_demo_sessions";

function getSavedSessions() {
    try {
        return JSON.parse(localStorage.getItem(STORAGE_KEY_SESSIONS)) || [];
    } catch (e) {
        return [];
    }
}

function getJWTToken() {
    return localStorage.getItem(STORAGE_KEY_JWT) || "";
}

async function fetchTenantMe(token) {
    if (!token) return null;
    try {
        const res = await fetch("/api/tenants/me", {
            headers: { "Authorization": "Bearer " + token }
        });
        if (!res.ok) return null;
        const data = await res.json();
        return (data && data.data) ? data.data : null;
    } catch (e) {
        return null;
    }
}

async function fetchUserRole(token) {
    if (!token) return null;
    const parsed = parseJWTToken(token);
    const userID = (parsed && (parsed.sub || parsed.user_id)) || "";
    if (!userID) return null;
    try {
        const res = await fetch(`/api/auth/users/${encodeURIComponent(userID)}/role`, {
            headers: { "Authorization": "Bearer " + token }
        });
        if (!res.ok) return null;
        const data = await res.json();
        return (data && data.data) ? data.data : null;
    } catch (e) {
        return null;
    }
}

function saveJWTToken(token, tenantInfo, userRole) {
    if (token) {
        localStorage.setItem(STORAGE_KEY_JWT, token);
        localStorage.setItem(STORAGE_KEY_PASSWORD_SET, "true");

        const parsed = parseJWTToken(token);
        if (parsed) {
            const email = parsed.email || parsed.sub || "user@example.com";
            const tenant_id = parsed.tenant_id || "unknown_tenant";
            const user_id = parsed.sub || parsed.user_id || "unknown_user";

            const sessions = getSavedSessions();
            const existingIdx = sessions.findIndex(s => (s.tenant_id === tenant_id && s.email === email) || s.token === token);
            const sessionItem = {
                token: token,
                email: email,
                tenant_id: tenant_id,
                user_id: user_id,
                tenant_name: tenantInfo ? tenantInfo.Name || tenantInfo.name : null,
                tenant_slug: tenantInfo ? tenantInfo.Slug || tenantInfo.slug : null,
                tenant_plan: tenantInfo ? tenantInfo.Plan || tenantInfo.plan : null,
                tenant_status: tenantInfo ? tenantInfo.Status || tenantInfo.status : null,
                owner_name: tenantInfo ? tenantInfo.OwnerName || tenantInfo.owner_name : null,
                owner_email: tenantInfo ? tenantInfo.OwnerEmail || tenantInfo.owner_email : null,
                role_name: userRole ? (userRole.role && (userRole.role.name || userRole.role.Name)) || userRole.role_name : null,
                role_permissions: userRole ? (userRole.role && (userRole.role.permissions || userRole.role.Permissions)) || userRole.role_permissions : null,
                updated_at: new Date().toISOString()
            };

            if (existingIdx >= 0) {
                sessions[existingIdx] = { ...sessions[existingIdx], ...sessionItem };
            } else {
                sessions.unshift(sessionItem);
            }
            localStorage.setItem(STORAGE_KEY_SESSIONS, JSON.stringify(sessions));
        }
    } else {
        localStorage.removeItem(STORAGE_KEY_JWT);
    }
}

function switchActiveSession(token) {
    if (token) {
        saveJWTToken(token);
        window.location.reload();
    }
}

function clearAllSessions() {
    localStorage.removeItem(STORAGE_KEY_JWT);
    localStorage.removeItem(STORAGE_KEY_SESSIONS);
    localStorage.removeItem(STORAGE_KEY_PASSWORD_SET);
    window.location.reload();
}

function parseJWTToken(token) {
    if (!token) return null;
    try {
        const base64Url = token.split(".")[1];
        const base64 = base64Url.replace(/-/g, "+").replace(/_/g, "/");
        const jsonPayload = decodeURIComponent(atob(base64).split("").map(c => {
            return "%" + ("00" + c.charCodeAt(0).toString(16)).slice(-2);
        }).join(""));
        return JSON.parse(jsonPayload);
    } catch (e) {
        return null;
    }
}

function getSavedTenants() {
    try {
        return JSON.parse(localStorage.getItem(STORAGE_KEY_TENANTS)) || [];
    } catch (e) {
        return [];
    }
}

function saveTenant(tenantObj) {
    const tenants = getSavedTenants();
    const existingIdx = tenants.findIndex(t => t.tenant_id === tenantObj.tenant_id);
    if (existingIdx >= 0) {
        tenants[existingIdx] = { ...tenants[existingIdx], ...tenantObj };
    } else {
        tenants.unshift(tenantObj);
    }
    localStorage.setItem(STORAGE_KEY_TENANTS, JSON.stringify(tenants));
    localStorage.setItem(STORAGE_KEY_REGISTERED, "true");
}

function updateTenantStatus(tenantId, newStatus) {
    const tenants = getSavedTenants();
    const t = tenants.find(item => item.tenant_id === tenantId);
    if (t && t.status !== newStatus) {
        t.status = newStatus;
        localStorage.setItem(STORAGE_KEY_TENANTS, JSON.stringify(tenants));
    }
}

function renderNavHeader(activeTabId) {
    initSessionState();
    injectResponsiveStyles();

    const sessions = getSavedSessions();
    const count = sessions.length;
    const activeToken = getJWTToken();
    const hasToken = !!activeToken;

    const registered = isRegistered();
    const mailClicked = isMailClicked();
    const passwordSet = isPasswordSet();

    const steps = [
        { id: "register", label: "1. Auth & Register", href: "/index.html", unlocked: true },
        { id: "mailbox", label: `2. Dev Mailbox ${registered ? '' : '(Locked)'}`, href: "/mailbox.html", unlocked: registered },
        { id: "setup-password", label: `3. Setup Password ${mailClicked ? '' : '(Locked)'}`, href: "/setup-password.html", unlocked: mailClicked },
        { id: "login", label: `4. Login ${passwordSet ? '' : '(Locked)'}`, href: "/login.html", unlocked: passwordSet },
        { id: "admin", label: `5. Administrative ${hasToken ? '' : '(Locked)'}`, href: "/administrative.html", unlocked: hasToken },
        { id: "orders", label: `6. Orders (Data Plane) ${hasToken ? '' : '(Locked)'}`, href: "/orders.html", unlocked: hasToken },
        { id: "notifications", label: `7. Notifications ${hasToken ? '' : '(Locked)'}`, href: "/notifications.html", unlocked: hasToken },
        { id: "registry", label: `8. Session Registry (${count})`, href: "/registry.html", unlocked: true }
    ];

    let links = [];
    steps.forEach((s) => {
        const isCurrentActive = activeTabId === s.id;
        const styleStr = isCurrentActive ? "font-weight: bold; text-decoration: underline;" : "";

        if (s.unlocked) {
            links.push(`<a href="${s.href}" style="${styleStr}">${s.label}</a>`);
        } else {
            links.push(`<span style="color: #999; cursor: not-allowed;" title="Complete prerequisite step to unlock">${s.label}</span>`);
        }
    });

    let accountSwitcherHtml = "";
    if (sessions.length > 0) {
        const options = sessions.map(s => {
            const isActive = s.token === activeToken;
            const label = s.tenant_name
                ? `${s.tenant_name}${s.tenant_slug ? ` / ${s.tenant_slug}` : ''}`
                : `Tenant: ${s.tenant_id}`;
            return `<option value="${s.token}" ${isActive ? 'selected' : ''}>${isActive ? '' : ''}${s.email} (${label})</option>`;
        }).join("");

        accountSwitcherHtml = `
            <div style="margin-top: 10px; padding: 8px 12px; background: #eef6ff; border: 1px solid #b6d4fe; border-radius: 4px; display: flex; align-items: center; gap: 10px; flex-wrap: wrap;">
                <label for="nav-account-switcher" style="">Active Session Context:</label>
                <select id="nav-account-switcher" onchange="switchActiveSession(this.value)" style="padding: 4px 8px; flex: 1; min-width: 250px;">
                    ${options}
                </select>
                <button onclick="clearAllSessions()" style="padding: 4px 8px; background: #dc3545; color: white; border: none; border-radius: 3px; cursor: pointer; white-space: nowrap;">Purge All Sessions</button>
            </div>
        `;
    }

    const navHtml = `
        <h1>Multi-Tenant Microservices Platform Dashboard</h1>
        <p>Routed through Traefik Gateway (port 8000). Authenticated via RS256 JWT Token. Status: ${hasToken ? '<b>[JWT Active]</b>' : '<i>[Unauthenticated]</i>'}</p>
        ${accountSwitcherHtml}
        <hr>
        <nav style="margin-bottom: 20px; line-height: 1.8;">
            ${links.join(" -> ")}
        </nav>
        <hr>
    `;

    const navContainer = document.getElementById("nav-container");
    if (navContainer) {
        navContainer.innerHTML = navHtml;
    }
}
