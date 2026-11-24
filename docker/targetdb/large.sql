
\if :{?rows}
\else
  \set rows 3000000
\endif

DROP TABLE IF EXISTS public.events;

CREATE TABLE public.events (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payload    TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'new',
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT events_status_check CHECK (status IN ('new', 'paid', 'shipped', 'cancelled'))
);

INSERT INTO public.events (payload, status, created_at)
SELECT md5(g::text),
       (ARRAY['new', 'paid', 'shipped', 'cancelled'])[g % 4 + 1],
       timestamptz '2026-01-01 00:00:00+00' + g * interval '1 second'
FROM generate_series(1, :rows) AS g;

ANALYZE public.events;

SELECT count(*) AS rows, pg_size_pretty(pg_total_relation_size('public.events')) AS size
FROM public.events;
