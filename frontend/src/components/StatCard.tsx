import type { ReactNode } from 'react'
import Icon, { type IconName } from './Icon'

interface StatCardProps {
  icon: IconName
  label: string
  value: ReactNode
  accent?: boolean
}

export default function StatCard({ icon, label, value, accent = false }: StatCardProps) {
  return (
    <div
      className={
        'min-w-0 rounded-xl px-6 pb-5 pt-[1.1rem] max-sm:px-5 ' +
        (accent ? 'border border-white/60 bg-butter shadow-card' : 'glass')
      }
    >
      <div
        className={
          'mb-2 flex items-center gap-2 text-[0.82rem] font-semibold ' + (accent ? 'text-butter-ink' : 'text-muted')
        }
      >
        <span className="grid h-[1.9em] w-[1.9em] place-items-center rounded-full bg-green-soft text-green">
          <Icon name={icon} className="h-[1em] w-[1em]" />
        </span>
        {label}
      </div>
      <div className="text-[clamp(2.2rem,4.5vw,3rem)] font-light leading-none tracking-[-0.05em] text-ink tabular-nums">
        {value}
      </div>
    </div>
  )
}
