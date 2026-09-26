-- Synthetic demo data for local development only. Never run in production.
-- Customers demo-alice and demo-bob; mint tokens with: make token SUB=demo-alice
INSERT INTO accounts (id, customer_id, currency, balance, status) VALUES
    ('a1111111-1111-4111-8111-111111111111', 'demo-alice', 'THB', 1000000, 'ACTIVE'), -- 10,000.00
    ('a2222222-2222-4222-8222-222222222222', 'demo-alice', 'THB',   50000, 'ACTIVE'), --    500.00
    ('b1111111-1111-4111-8111-111111111111', 'demo-bob',   'THB',  200000, 'ACTIVE'), --  2,000.00
    ('b2222222-2222-4222-8222-222222222222', 'demo-bob',   'THB',       0, 'FROZEN')
ON CONFLICT (id) DO NOTHING;
