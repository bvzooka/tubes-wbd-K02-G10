CREATE TABLE customers (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orders (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    total       NUMERIC(12, 2) NOT NULL CHECK (total >= 0),
    status      TEXT NOT NULL DEFAULT 'new',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_customer_idx ON orders (customer_id);

CREATE VIEW order_totals AS
SELECT customer_id, sum(total) AS total_spent FROM orders GROUP BY customer_id;

INSERT INTO customers (name, email)
SELECT 'Customer ' || g, 'customer' || g || '@example.com' FROM generate_series(1, 120) g;

INSERT INTO orders (customer_id, total)
SELECT (g % 120) + 1, (g * 7) % 500 FROM generate_series(1, 300) g;
