import type { ReactNode } from 'react'
import Icon, { type IconName } from './Icon'

interface PanelProps {
  icon: IconName
  title: string
  count?: number
  footer?: ReactNode
  danger?: boolean
  children: ReactNode
}

export default function Panel({ icon, title, count, footer, danger = false, children }: PanelProps) {
  return (
    <section
      className={
        'mb-5 overflow-hidden rounded-xl border bg-paper shadow-card ' + (danger ? 'border-danger/35' : 'border-white/80')
      }
    >
      <div className="flex items-center justify-between gap-4 border-b border-line px-6 py-4 max-md:px-4">
        <h2 className="m-0 flex min-w-0 items-center gap-2.5 text-[1.1rem]">
          <span
            className={
              'grid h-[1.6em] w-[1.6em] flex-none place-items-center rounded-full ' +
              (danger ? 'bg-danger-soft text-danger' : 'bg-ink text-lime')
            }
          >
            <Icon name={icon} className="h-[0.95em] w-[0.95em]" />
          </span>
          {title}
        </h2>
        {count !== undefined && (
          <span className="flex-none rounded-full border border-line bg-paper-soft px-3 py-0.5 text-[0.82rem] font-bold text-muted">
            {count}
          </span>
        )}
      </div>
      <div className="relative overflow-x-auto">{children}</div>
      {footer && <div className="border-t border-line bg-paper-soft px-6 py-5 max-md:px-4">{footer}</div>}
    </section>
  )
}
