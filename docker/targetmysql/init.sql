SET NAMES utf8mb4;
SET SESSION cte_max_recursion_depth = 1000;
USE sampledb;

CREATE TABLE customers (
    id INT NOT NULL AUTO_INCREMENT,
    full_name VARCHAR(100) NOT NULL,
    email VARCHAR(190) NOT NULL,
    balance DECIMAL(12, 2) NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    profile JSON NULL,
    tags SET('vip', 'new', 'wholesale', 'blocked') NULL,
    notes TEXT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    CONSTRAINT customers_email_key UNIQUE (email),
    CONSTRAINT customers_balance_nonneg CHECK (balance >= 0),
    KEY customers_name_idx (full_name(20)),
    KEY customers_lower_name_idx ((LOWER(full_name))),
    KEY customers_created_idx (created_at DESC),
    FULLTEXT KEY customers_notes_ft (notes)
) ENGINE=InnoDB;

CREATE TABLE products (
    sku VARCHAR(20) NOT NULL,
    name VARCHAR(200) NOT NULL,
    price DECIMAL(10, 2) NOT NULL,
    weight_kg FLOAT NULL,
    thumbnail BLOB NULL,
    released_on DATE NULL,
    PRIMARY KEY (sku),
    CONSTRAINT products_price_check CHECK (price > 0)
) ENGINE=InnoDB;

CREATE TABLE orders (
    id BIGINT NOT NULL AUTO_INCREMENT,
    customer_id INT NOT NULL,
    status VARCHAR(10) NOT NULL DEFAULT 'new',
    note TEXT NULL,
    ordered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY orders_customer_idx (customer_id, ordered_at DESC),
    CONSTRAINT orders_customer_fk FOREIGN KEY (customer_id)
        REFERENCES customers (id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT orders_status_check CHECK (status IN ('new', 'paid', 'shipped', 'cancelled'))
) ENGINE=InnoDB;

CREATE TABLE order_items (
    order_id BIGINT NOT NULL,
    sku VARCHAR(20) NOT NULL,
    quantity INT NOT NULL DEFAULT 1,
    PRIMARY KEY (order_id, sku),
    CONSTRAINT order_items_order_fk FOREIGN KEY (order_id)
        REFERENCES orders (id) ON DELETE CASCADE,
    CONSTRAINT order_items_product_fk FOREIGN KEY (sku)
        REFERENCES products (sku),
    CONSTRAINT order_items_qty_check CHECK (quantity > 0)
) ENGINE=InnoDB;

-- tabel untuk uji pagination (250 baris)
CREATE TABLE event_log (
    id INT NOT NULL,
    message VARCHAR(100) NOT NULL,
    detail TEXT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

-- nama berspasi dan huruf besar harus dikutip dengan benar
CREATE TABLE `Mixed Case` (
    `Id` INT NOT NULL,
    `Some Value` TEXT NULL,
    PRIMARY KEY (`Id`)
) ENGINE=InnoDB;

-- satu baris per tipe data umum, termasuk NULL
CREATE TABLE type_samples (
    id INT NOT NULL AUTO_INCREMENT,
    c_tinyint TINYINT NULL,
    c_smallint SMALLINT NULL,
    c_bigint_unsigned BIGINT UNSIGNED NULL,
    c_float FLOAT NULL,
    c_double DOUBLE NULL,
    c_decimal DECIMAL(20, 6) NULL,
    c_char CHAR(5) NULL,
    c_varchar VARCHAR(50) NULL,
    c_longtext LONGTEXT NULL,
    c_blob BLOB NULL,
    c_bit BIT(8) NULL,
    c_date DATE NULL,
    c_time TIME NULL,
    c_datetime DATETIME(3) NULL,
    c_timestamp TIMESTAMP NULL,
    c_year YEAR NULL,
    c_enum ENUM('kecil', 'sedang', 'besar') NOT NULL DEFAULT 'sedang',
    c_json JSON NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;


INSERT INTO customers (full_name, email, balance, is_active, profile, tags, notes)
WITH RECURSIVE seq (n) AS (
    SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 120
)
SELECT
    CONCAT(ELT(n % 6 + 1, 'Budi', 'Siti', 'Agus', 'Dewi', 'Rizky', 'Putri'), ' ',
           ELT(n % 5 + 1, 'Santoso', 'Rahmawati', 'Pratama', 'Lestari', 'Wijaya'), ' ', n),
    CONCAT('user', n, '@mail.example'),
    (n * 7919) % 100000 / 100,
    n % 7 <> 0,
    IF(n % 3 = 0, NULL, JSON_OBJECT('city', ELT(n % 4 + 1, 'Bandung', 'Jakarta', 'Surabaya', 'Medan'), 'level', n % 5)),
    IF(n % 4 = 0, 'vip,new', IF(n % 9 = 0, 'wholesale', NULL)),
    IF(n % 10 = 0, REPEAT('Catatan panjang untuk menguji pemotongan teks di tampilan. ', 8), IF(n % 2 = 0, 'Pelanggan setia ✓', NULL))
FROM seq;

INSERT INTO products (sku, name, price, weight_kg, thumbnail, released_on) VALUES
    ('SKU-001', 'Keyboard Mekanik', 450000.00, 0.95, UNHEX('89504E470D0A1A0A'), '2024-03-01'),
    ('SKU-002', 'Mouse Nirkabel', 175000.00, 0.08, NULL, '2024-05-20'),
    ('SKU-003', 'Monitor 24"', 1899000.00, 3.4, NULL, NULL),
    ('SKU-004', 'Kabel USB-C', 35000.50, NULL, NULL, '2023-11-11'),
    ('SKU-005', 'Webcam Full HD', 520000.00, 0.2, NULL, '2025-01-15');

INSERT INTO orders (customer_id, status, note, ordered_at)
WITH RECURSIVE seq (n) AS (
    SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 300
)
SELECT
    n % 120 + 1,
    ELT(n % 4 + 1, 'new', 'paid', 'shipped', 'cancelled'),
    IF(n % 6 = 0, 'Kirim pagi hari', NULL),
    TIMESTAMP('2025-01-01 08:00:00') + INTERVAL n * 7 HOUR
FROM seq;

INSERT INTO order_items (order_id, sku, quantity)
SELECT o.id, p.sku, (o.id + p.id_n) % 3 + 1
FROM orders o
JOIN (SELECT sku, ROW_NUMBER() OVER (ORDER BY sku) AS id_n FROM products) p
  ON p.id_n <= (o.id % 3) + 1;

INSERT INTO event_log (id, message, detail)
WITH RECURSIVE seq (n) AS (
    SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 250
)
SELECT n, CONCAT('event ', n), IF(n % 5 = 0, NULL, CONCAT('detail untuk event ', n)) FROM seq;

INSERT INTO `Mixed Case` (`Id`, `Some Value`) VALUES
    (1, 'nilai pertama'),
    (2, NULL),
    (3, '<script>alert("xss")</script> & "kutip" ''tunggal''');

INSERT INTO type_samples (c_tinyint, c_smallint, c_bigint_unsigned, c_float, c_double, c_decimal, c_char, c_varchar,
                          c_longtext, c_blob, c_bit, c_date, c_time, c_datetime, c_timestamp, c_year, c_enum, c_json)
VALUES
    (-128, 32767, 18446744073709551615, 1.5, 3.141592653589793, 1234567890.123456, 'abc', 'teks biasa',
     REPEAT('x', 300), UNHEX('DEADBEEF'), b'10101010', '2025-10-24', '13:45:10', '2025-10-24 13:45:10.123',
     '2025-10-24 13:45:10', 2025, 'besar', JSON_OBJECT('a', JSON_ARRAY(1, 2, 3))),
    (NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, 'kecil', NULL),
    (0, 0, 0, 0, 0, 0, '', '', '', '', b'0', '0001-01-01', '-838:59:59', '1000-01-01 00:00:00.000',
     '2038-01-19 03:14:07', 1901, 'sedang', JSON_QUOTE('string json'));

CREATE VIEW active_customers AS
    SELECT id, full_name, email FROM customers WHERE is_active;

CREATE VIEW order_totals AS
    SELECT o.id AS order_id, SUM(p.price * i.quantity) AS total
    FROM orders o
    JOIN order_items i ON i.order_id = o.id
    JOIN products p ON p.sku = i.sku
    GROUP BY o.id;

-- Perbarui statistik supaya estimasi baris (information_schema.TABLES) terisi.
ANALYZE TABLE customers, products, orders, order_items, event_log, `Mixed Case`, type_samples;
