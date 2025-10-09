const CURRENT_SESSION_ID = "{{SERVER_SESSION_ID}}";
const STORAGE_KEY_TENANTS = "broker_demo_tenants";
const STORAGE_KEY_SESSION = "broker_demo_session_id";

function initSessionState() {
    const savedSession = localStorage.getItem(STORAGE_KEY_SESSION);
    if (savedSession !== CURRENT_SESSION_ID) {
        localStorage.clear();
        localStorage.setItem(STORAGE_KEY_SESSION, CURRENT_SESSION_ID);
        localStorage.setItem(STORAGE_KEY_TENANTS, JSON.stringify([]));
        console.log("Fresh server session detected. Local state purged cleanly.");
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

    const navHtml = `
        <h1>Multi-Tenant Microservices Flow Demonstration</h1>
        <p>Routed through Traefik Gateway (port 8000).</p>
        <hr>
        <nav style="margin-bottom: 20px;">
            <a href="/index.html" style="${activeTabId==='register'?'font-weight:bold;':''}">1. Register Tenant (Control Plane)</a> | 
            <a href="/mailbox.html" style="${activeTabId==='mailbox'?'font-weight:bold;':''}">2. Dev Mailpit Inbox</a> | 
            <a href="/orders.html" style="${activeTabId==='orders'?'font-weight:bold;':''}">3. Orders (Data Plane)</a> | 
            <a href="/registry.html" style="${activeTabId==='registry'?'font-weight:bold;':''}">4. Session Registry (${count})</a>
        </nav>
        <hr>
    `;
    const navContainer = document.getElementById("nav-container");
    if (navContainer) {
        navContainer.innerHTML = navHtml;
    }
}
