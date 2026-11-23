import type { ReactNode } from 'react'
import FieldError from './FieldError'

interface FieldProps {
  label: string
  error?: string
  hint?: string
  wide?: boolean
  children: ReactNode
}


export default function Field({ label, error, hint, wide = false, children }: FieldProps) {
  return (
    <label className={'block min-w-0' + (wide ? ' col-span-full' : '')}>
      <span className="mb-[0.35rem] block">{label}</span>
      {children}
      {hint && <span className="hint">{hint}</span>}
      <FieldError message={error} />
    </label>
  )
}
