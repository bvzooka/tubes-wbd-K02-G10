interface SqlBlockProps {
  title: string
  sql: string
}

// SQL panjang menggulir di dalam kotaknya sendiri supaya halaman tidak ikut melebar
export default function SqlBlock({ title, sql }: SqlBlockProps) {
  return (
    <div className="min-w-0">
      <h3 className="mb-1.5 mt-0 text-[0.95rem]">{title}</h3>
      <pre className="m-0 max-h-72 overflow-auto rounded-md bg-ink p-3 font-mono text-xs text-paper-soft">
        {sql || '(belum diisi)'}
      </pre>
    </div>
  )
}
