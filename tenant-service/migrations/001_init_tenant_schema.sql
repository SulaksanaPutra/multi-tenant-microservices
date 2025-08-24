-- Migration template for dynamic tenant schema: {{SCHEMA_NAME}}

CREATE TABLE IF NOT EXISTS {{SCHEMA_NAME}}.tenant_settings (
    id SERIAL PRIMARY KEY,
    setting_key VARCHAR(100) NOT NULL UNIQUE,
    setting_value TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS {{SCHEMA_NAME}}.tenant_members (
    id SERIAL PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL DEFAULT 'owner',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);


-- Default Seed Data
INSERT INTO {{SCHEMA_NAME}}.tenant_settings (setting_key, setting_value)
VALUES 
    ('plan', 'pro'),
    ('status', 'active')
ON CONFLICT (setting_key) DO NOTHING;
