import type { ReactNode } from 'react'
export type BadgeTone = 'neutral' | 'outline' | 'lime' | 'butter' | 'green' | 'red' | 'amber' | 'ink'

interface BadgeProps {
  tone?: BadgeTone
  dot?: boolean
  children: ReactNode
}

const toneClass: Record<BadgeTone, string> = {
  neutral: '',
  outline: 'badge-outline',
  lime: 'badge-lime',
  butter: 'badge-butter',
  green: 'badge-green',
  red: 'badge-red',
  amber: 'badge-amber',
  ink: 'badge-ink',
}

export default function Badge({ tone = 'neutral', dot = false, children }: BadgeProps) {
  return <span className={['badge', toneClass[tone], dot ? 'badge-dot' : ''].join(' ')}>{children}</span>
}
