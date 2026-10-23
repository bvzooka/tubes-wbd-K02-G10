CREATE TABLE customers (
    id  INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    full_name VARCHAR(100) NOT NULL,
    email TEXT NOT NULL,
    balance NUMERIC(12, 2) NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    profile JSONB,
    tags TEXT[],
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT customers_email_key UNIQUE (email),
    CONSTRAINT customers_balance_nonneg CHECK (balance >= 0)
);
CREATE INDEX customers_name_idx ON customers (lower(full_name));
CREATE INDEX customers_active_idx ON customers (id) WHERE is_active;
CREATE INDEX customers_tags_gin ON customers USING gin (tags);

CREATE TABLE products (
    sku VARCHAR(20) PRIMARY KEY,
    name TEXT NOT NULL,
    price NUMERIC(10, 2) NOT NULL CHECK (price > 0),
    weight_kg REAL,
    thumbnail BYTEA,
    released_on DATE
);

CREATE TABLE orders (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'new',
    note TEXT,
    ordered_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT orders_customer_fk FOREIGN KEY (customer_id)
        REFERENCES customers (id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT orders_status_check CHECK (status IN ('new', 'paid', 'shipped', 'cancelled'))
);
CREATE INDEX orders_customer_idx ON orders (customer_id, ordered_at DESC);

CREATE TABLE order_items (
    order_id BIGINT NOT NULL,
    sku VARCHAR(20) NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (order_id, sku),
    CONSTRAINT order_items_order_fk FOREIGN KEY (order_id)
        REFERENCES orders (id) ON DELETE CASCADE,
    CONSTRAINT order_items_product_fk FOREIGN KEY (sku)
        REFERENCES products (sku),
    CONSTRAINT order_items_qty_check CHECK (quantity > 0)
);

-- tabel untuk uji pagination 
CREATE TABLE event_log (
    id INTEGER PRIMARY KEY,
    message TEXT NOT NULL,
    detail TEXT
);

-- nama berspasi dan huruf besar hrs di-quote dgn benar
CREATE TABLE "Mixed Case" (
    "Id" INTEGER PRIMARY KEY,
    "Some Value" TEXT
);

CREATE VIEW active_customers AS
    SELECT id, full_name, email FROM customers WHERE is_active;

CREATE MATERIALIZED VIEW order_totals AS
    SELECT o.id AS order_id, sum(p.price * i.quantity) AS total
    FROM orders o
    JOIN order_items i ON i.order_id = o.id
    JOIN products p ON p.sku = i.sku
    GROUP BY o.id;

CREATE SCHEMA hr;
CREATE TABLE hr.employees (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    manager_id INTEGER REFERENCES hr.employees (id),
    salary NUMERIC(10, 2) CHECK (salary IS NULL OR salary >= 0),
    CONSTRAINT employees_name_key UNIQUE (name)
);

-- Data

INSERT INTO customers (full_name, email, balance, is_active, profile, tags) VALUES
    ('Alice Wijaya', 'alice@example.com', 1500000.50, TRUE,  '{"city": "Bandung", "vip": true}', ARRAY['vip', 'early']),
    ('Budi Santoso', 'budi@example.com',  250000.00,  TRUE,  '{"city": "Jakarta"}', ARRAY['regular']),
    ('Citra Dewi', 'citra@example.com', 0, FALSE, NULL, NULL),
    ('Dedi <script>alert(1)</script>', 'dedi@example.com', 10.25, TRUE, '{}', ARRAY[]::text[]);

INSERT INTO products (sku, name, price, weight_kg, thumbnail, released_on) VALUES
    ('KBD-001', 'Keyboard Mekanik', 450000.00, 0.9, '\xdeadbeef', '2025-03-01'),
    ('MSE-002', 'Mouse Nirkabel',   175000.00, 0.1, NULL, '2025-06-15'),
    ('MON-003', 'Monitor 24"', 2100000.00, 3.4, NULL, NULL);

INSERT INTO orders (customer_id, status, note) VALUES
    (1, 'paid', 'Kirim cepat'),
    (1, 'new', NULL),
    (2, 'shipped', 'Catatan dengan karakter ''kutip'' dan "ganda"');

INSERT INTO order_items (order_id, sku, quantity) VALUES
    (1, 'KBD-001', 1), (1, 'MSE-002', 2), (3, 'MON-003', 1);

INSERT INTO event_log (id, message, detail)
SELECT g, 'event ' || g, CASE WHEN g % 10 = 0 THEN NULL ELSE repeat('x', 5) END
FROM generate_series(1, 250) AS g;

INSERT INTO "Mixed Case" ("Id", "Some Value") VALUES (1, 'satu'), (2, NULL);

INSERT INTO hr.employees (id, name, manager_id, salary) VALUES
    (1, 'Bos Besar', NULL, 20000000),
    (2, 'Staf Satu', 1, 8000000),
    (3, 'Staf Dua',  1, NULL);

REFRESH MATERIALIZED VIEW order_totals;
ANALYZE;
