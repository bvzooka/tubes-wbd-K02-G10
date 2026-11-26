
\set ON_ERROR_STOP on
\if :{?rows}
\else
  \set rows 200000
\endif

DROP DATABASE IF EXISTS k6_shared WITH (FORCE);
DROP DATABASE IF EXISTS k6_db_1 WITH (FORCE);
DROP DATABASE IF EXISTS k6_db_2 WITH (FORCE);
DROP DATABASE IF EXISTS k6_db_3 WITH (FORCE);
DROP DATABASE IF EXISTS k6_db_4 WITH (FORCE);
DROP DATABASE IF EXISTS k6_template WITH (FORCE);

CREATE DATABASE k6_template;
\connect k6_template

CREATE TABLE public.k6_items (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    counter BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ
);
INSERT INTO public.k6_items (counter) SELECT 0 FROM generate_series(1, :rows);
ANALYZE public.k6_items;

\connect postgres
CREATE DATABASE k6_shared TEMPLATE k6_template;
CREATE DATABASE k6_db_1 TEMPLATE k6_template;
CREATE DATABASE k6_db_2 TEMPLATE k6_template;
CREATE DATABASE k6_db_3 TEMPLATE k6_template;
CREATE DATABASE k6_db_4 TEMPLATE k6_template;
