import type { ReactNode } from 'react'
import Icon, { type IconName } from './Icon'

export type SectionTone = 'green' | 'butter' | 'ink'

const stepClass: Record<SectionTone, string> = {
  green: 'bg-green-soft text-green',
  butter: 'bg-butter text-butter-ink',
  ink: 'bg-ink text-lime',
}

interface FormSectionProps {
  icon: IconName
  tone: SectionTone
  title: string
  desc?: string
  columns?: 1 | 2
  children: ReactNode
}

export default function FormSection({ icon, tone, title, desc, columns = 2, children }: FormSectionProps) {
  return (
    <fieldset className="m-0 grid min-w-0 grid-cols-[46px_minmax(0,1fr)] gap-x-[1.1rem] border-0 border-b border-solid border-line px-[1.6rem] py-6 max-md:grid-cols-1 max-md:px-5">
      <span className={'grid h-[46px] w-[46px] place-items-center rounded-full max-md:hidden ' + stepClass[tone]}>
        <Icon name={icon} className="h-[22px] w-[22px]" />
      </span>
      <div className="min-w-0">
        <legend className="float-left m-0 mt-0.5 w-full p-0 text-[1.1rem] font-bold tracking-[-0.015em] text-ink">
          {title}
        </legend>
        {desc ? (
          <p className="clear-both mb-[1.1rem] mt-0.5 pt-0.5 text-[0.88rem] text-muted">{desc}</p>
        ) : (
          <div className="clear-both mb-3" />
        )}
        <div className={'grid gap-x-[1.1rem] gap-y-4 ' + (columns === 2 ? 'grid-cols-2 max-md:grid-cols-1' : '')}>
          {children}
        </div>
      </div>
    </fieldset>
  )
}
