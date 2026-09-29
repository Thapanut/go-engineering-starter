-- Local development only: synthetic demo products (same as memory.SampleProducts).
-- Runs after the migrations on the first start of the dev database.
INSERT INTO catalog.products (id, name, price_minor, currency, active) VALUES
    ('COFFEE-BEANS-250G', 'Arabica Coffee Beans 250 g', 45000, 'THB', true),
    ('CERAMIC-MUG',       'Ceramic Mug 350 ml',         29000, 'THB', true),
    ('POUR-OVER-DRIPPER', 'Pour-over Dripper',          26000, 'THB', true),
    ('HAND-GRINDER',      'Hand Grinder (discontinued)', 150000, 'THB', false)
ON CONFLICT (id) DO NOTHING;
