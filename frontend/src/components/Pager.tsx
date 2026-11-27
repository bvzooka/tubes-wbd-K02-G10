interface PagerProps {
  page: number
  hasNext: boolean
  onChange: (page: number) => void
  disabled?: boolean
}

export default function Pager({ page, hasNext, onChange, disabled = false }: PagerProps) {
  const off = 'btn border-dashed border-line-strong bg-white/60 font-semibold text-muted'
  // di ponsel huruf dan jarak dalam tombol dikecilkan supaya ketiganya muat dalam satu baris
  const narrow = ' max-sm:px-2.5 max-sm:text-[0.82rem]'
  return (
    <nav aria-label="Halaman" className="my-6 flex flex-wrap items-center justify-center gap-2.5 max-sm:gap-1.5">
      <button
        type="button"
        className={(page <= 1 ? off : 'btn-primary') + narrow}
        disabled={disabled || page <= 1}
        onClick={() => onChange(page - 1)}
      >
        Sebelumnya
      </button>
      <span className="glass inline-flex min-h-[44px] items-center whitespace-nowrap rounded-full px-5 font-bold text-ink max-sm:px-2.5 max-sm:text-[0.82rem]">
        Halaman {page}
      </span>
      <button
        type="button"
        className={(!hasNext ? off : 'btn-primary') + narrow}
        disabled={disabled || !hasNext}
        onClick={() => onChange(page + 1)}
      >
        Berikutnya
      </button>
    </nav>
  )
}
