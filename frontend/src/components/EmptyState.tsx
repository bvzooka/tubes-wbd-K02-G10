import type { ReactNode } from 'react'
import Icon, { type IconName } from './Icon'

interface EmptyStateProps {
  message: string
  title?: string
  icon?: IconName
  action?: ReactNode
  plain?: boolean
}

export default function EmptyState({ message, title, icon, action, plain = false }: EmptyStateProps) {
  return (
    <div className={plain ? 'px-6 py-10 text-center text-muted' : 'empty'}>
      {icon && <Icon name={icon} className="mx-auto mb-2.5 h-[46px] w-[46px] text-green" />}
      {title && <strong className="mb-1 block text-[1.15rem] text-ink">{title}</strong>}
      <p className="m-0">{message}</p>
      {action && <div className="mt-5 flex justify-center">{action}</div>}
    </div>
  )
}
