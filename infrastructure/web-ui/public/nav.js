const CURRENT_SESSION_ID = "{{SERVER_SESSION_ID}}";
const STORAGE_KEY_TENANTS = "broker_demo_tenants";
const STORAGE_KEY_SESSION = "broker_demo_session_id";
const STORAGE_KEY_JWT = "broker_demo_jwt_token";

function initSessionState() {
    const savedSession = localStorage.getItem(STORAGE_KEY_SESSION);
    if (savedSession !== CURRENT_SESSION_ID) {
        localStorage.clear();
        localStorage.setItem(STORAGE_KEY_SESSION, CURRENT_SESSION_ID);
        localStorage.setItem(STORAGE_KEY_TENANTS, JSON.stringify([]));
        localStorage.removeItem(STORAGE_KEY_JWT);
        console.log("Fresh server session detected. Local state purged cleanly.");
    }
}

function getJWTToken() {
    return localStorage.getItem(STORAGE_KEY_JWT) || "";
}

function saveJWTToken(token) {
    if (token) {
        localStorage.setItem(STORAGE_KEY_JWT, token);
    } else {
        localStorage.removeItem(STORAGE_KEY_JWT);
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
    const tenants = getSavedTenants();
    const count = tenants.length;
    const hasToken = !!getJWTToken();

    const navHtml = `
        <h1>Multi-Tenant Microservices Platform Dashboard</h1>
        <p>Routed through Traefik Gateway (port 8000). Authenticated via RS256 JWT Token.</p>
        <hr>
        <nav style="margin-bottom: 20px; line-height: 1.8;">
            <a href="/index.html" style="${activeTabId==='register'?'font-weight:bold;':''}">1. Auth & Register</a> | 
            <a href="/users.html" style="${activeTabId==='users'?'font-weight:bold;':''}">2. Users & Roles ${hasToken ? '🔒' : '⚠️'}</a> | 
            <a href="/tenant.html" style="${activeTabId==='tenant'?'font-weight:bold;':''}">3. Tenant Info & Plan ${hasToken ? '🔒' : '⚠️'}</a> | 
            <a href="/notifications.html" style="${activeTabId==='notifications'?'font-weight:bold;':''}">4. Notifications ${hasToken ? '🔒' : '⚠️'}</a> | 
            <a href="/orders.html" style="${activeTabId==='orders'?'font-weight:bold;':''}">5. Orders (Data Plane) ${hasToken ? '🔒' : '⚠️'}</a> | 
            <a href="/mailbox.html" style="${activeTabId==='mailbox'?'font-weight:bold;':''}">6. Dev Mailpit Inbox</a> | 
            <a href="/registry.html" style="${activeTabId==='registry'?'font-weight:bold;':''}">7. Session Registry (${count})</a>
        </nav>
        <hr>
    `;
    const navContainer = document.getElementById("nav-container");
    if (navContainer) {
        navContainer.innerHTML = navHtml;
    }
}
