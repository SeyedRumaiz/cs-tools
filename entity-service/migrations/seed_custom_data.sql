BEGIN;

-- ============================================================================
-- 1. USERS
-- Types: 'internal', 'customer', 'system'
-- ============================================================================
INSERT INTO users (id, user_name, first_name, last_name, email, phone, timezone, user_type)
VALUES 
  ('10000000-0000-0000-0000-000000000001', 'john.doe2', 'John', 'Doe', 'john.doe2@example.com', '+1234567890', 'UTC', 'customer'),
  ('20000000-0000-0000-0000-000000000002', 'csm.engineer2', 'Alice', 'Smith', 'alice.smith2@wso2.com', '+1987654321', 'UTC', 'internal')
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 2. ACCOUNTS
-- Tiers: 'basic', 'enterprise'
-- ============================================================================
INSERT INTO accounts (id, sf_id, name, tier, region, activation_date, owner_id, technical_owner_id, agent_enabled, kb_references_enabled)
VALUES 
  ('30000000-0000-0000-0000-000000000001', 'SF-ACC-002', 'Acme Corporation', 'enterprise', 'NA-EAST', NOW(), '20000000-0000-0000-0000-000000000002', '20000000-0000-0000-0000-000000000002', true, true)
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 3. PROJECTS
-- Subscriptions: 'development_support', 'managed_cloud_subscription', 'subscription', etc.
-- ============================================================================
INSERT INTO projects (id, account_id, sf_id, name, key, subscription_type, closure_status, start_date, end_date)
VALUES 
  ('40000000-0000-0000-0000-000000000001', '30000000-0000-0000-0000-000000000001', 'SF-PRJ-002', 'Acme Cloud Integration', 'ACME-CLOUD-2', 'managed_cloud_subscription', 'open', CURRENT_DATE - INTERVAL '30 days', CURRENT_DATE + INTERVAL '335 days')
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 4. PRODUCTS
-- Class: 'software', 'service'
-- ============================================================================
INSERT INTO products (id, name, class)
VALUES 
  ('11111111-1111-1111-1111-111111111111', 'WSO2 API Manager', 'software')
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 5. PRODUCT VERSIONS (Required for Software products)
-- Status: 'available', 'deprecated', 'extended', 'discontinued'
-- ============================================================================
INSERT INTO product_versions (id, product_id, version, current_support_status, release_date)
VALUES 
  ('22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', '4.2.0', 'available', '2023-01-15')
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 6. DEPLOYMENTS
-- Types: 'primary_production', 'staging', 'qa', 'stress', 'uat', 'development'
-- ============================================================================
INSERT INTO deployments (id, project_id, name, type, description, created_by)
VALUES 
  ('33333333-3333-3333-3333-333333333333', '40000000-0000-0000-0000-000000000001', 'Production Gateway Cluster', 'primary_production', 'Main API Production deployment', '20000000-0000-0000-0000-000000000002')
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 7. DEPLOYED PRODUCTS
-- Links deployment to product & version
-- ============================================================================
INSERT INTO deployed_products (id, deployment_id, product_id, product_version_id)
VALUES 
  ('44444444-4444-4444-4444-444444444444', '33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222')
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 8. CASES
-- Types: 'case', 'service_request', 'security_report_analysis', 'engagement'
-- Severity: 'catastrophic', 'critical', 'high', 'medium', 'low'
-- State: 'open', 'work_in_progress', 'waiting_on_wso2', 'awaiting_info', 'closed'
-- ============================================================================
INSERT INTO cases (
  id, created_by, project_id, deployment_id, deployed_product_id,
  type, subject, description, severity, issue_type, state, assigned_engineer
)
VALUES (
  '55555555-5555-5555-5555-555555555555',
  '10000000-0000-0000-0000-000000000001',
  '40000000-0000-0000-0000-000000000001',
  '33333333-3333-3333-3333-333333333333',
  '44444444-4444-4444-4444-444444444444',
  'case',
  'High latency observed on API Gateway endpoint',
  'Detailed description of performance degradation on production gateway.',
  'high',
  'performance_degradation',
  'open',
  '20000000-0000-0000-0000-000000000002'
)
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- 9. CASE COMMENTS
-- Types: 'comment', 'work_note', 'activity'
-- Note: 'work_note' requires created_by to be an 'internal' user type
-- ============================================================================
INSERT INTO case_comments (id, case_id, type, content, created_by)
VALUES 
  ('66666666-6666-6666-6666-666666666666', '55555555-5555-5555-5555-555555555555', 'comment', 'Investigating thread dump logs provided.', '20000000-0000-0000-0000-000000000002')
ON CONFLICT (id) DO NOTHING;

COMMIT;
