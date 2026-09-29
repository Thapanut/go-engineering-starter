-- Local development only: synthetic demo products (same as memory.SampleProducts).
-- Runs after the migrations on the first start of the dev database.
INSERT INTO catalog.products (id, sku, name, price_minor, currency, active) VALUES
    ('04abef6a-166a-45f1-8004-904d9607a857', 'COFFEE-BEANS-250G', 'Arabica Coffee Beans 250 g',  45000,  'THB', true),
    ('959e6207-8780-45c0-885b-be846a8f147f', 'CERAMIC-MUG',       'Ceramic Mug 350 ml',          29000,  'THB', true),
    ('ff93e10e-646d-4dda-9072-601f73c69c0a', 'POUR-OVER-DRIPPER', 'Pour-over Dripper',           26000,  'THB', true),
    ('bdb770cd-3bbe-4fe0-a0c6-2bea0db94c1c', 'HAND-GRINDER',      'Hand Grinder (discontinued)', 150000, 'THB', false)
ON CONFLICT (id) DO NOTHING;
