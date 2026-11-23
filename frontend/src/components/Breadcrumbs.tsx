import { Link } from 'react-router-dom'

export interface Crumb {
  label: string
  to?: string
}

interface BreadcrumbsProps {
  items: Crumb[]
}

// Breadcrumbs: item terakhir adalah halaman saat ini, jadi tidak diberi link dan ditebalkan.
export default function Breadcrumbs({ items }: BreadcrumbsProps) {
  return (
    <nav aria-label="Breadcrumb" className="mb-[1.1rem] text-[0.88rem] text-muted">
      <ol className="flex flex-wrap items-center gap-1.5">
        {items.map((item, i) => {
          const last = i === items.length - 1
          return (
            <li key={i} className="flex min-w-0 items-center gap-1.5">
              {i > 0 && (
                <span aria-hidden="true" className="opacity-45">
                  /
                </span>
              )}
              {item.to && !last ? (
                <Link to={item.to} className="break-all px-0.5 font-medium text-muted hover:text-green">
                  {item.label}
                </Link>
              ) : (
                <span className="break-all font-bold text-ink" aria-current={last ? 'page' : undefined}>
                  {item.label}
                </span>
              )}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
