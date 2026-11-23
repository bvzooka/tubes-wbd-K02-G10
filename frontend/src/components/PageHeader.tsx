import type { ReactNode } from 'react'
import Icon, { type IconName } from './Icon'

interface PageHeaderProps {
  title: ReactNode
  // Pil kecil di atas judul: konteks halaman (misalnya DBMS atau "Administrasi").
  eyebrow?: { icon: IconName; label: ReactNode }
  subtitle?: ReactNode
  // Tombol aksi di bawah subjudul
  action?: ReactNode
  // Isi di samping kanan judul 
  aside?: ReactNode
  // elemen kecil samping judul
  titleExtra?: ReactNode
}

export default function PageHeader({ title, eyebrow, subtitle, action, aside, titleExtra }: PageHeaderProps) {
  return (
    <section className="mb-8 flex flex-wrap items-end justify-between gap-x-12 gap-y-6">
      <div className="min-w-0 max-w-full">
        {eyebrow && (
          <span className="eyebrow">
            <Icon name={eyebrow.icon} />
            {eyebrow.label}
          </span>
        )}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <h1 className="m-0 break-words text-[clamp(2rem,5vw,3.4rem)] font-light leading-[1.05] tracking-[-0.04em]">
            {title}
          </h1>
          {titleExtra}
        </div>
        {subtitle && <p className="mb-0 mt-3 max-w-[56ch] text-muted">{subtitle}</p>}
        {action && <div className="mt-5 flex flex-wrap gap-2.5">{action}</div>}
      </div>
      {/* Kartu angka di samping judul diberi lebar minimum; di mobile tampil dua kolom berdampingan. */}
      {aside && (
        <div className="flex flex-wrap gap-6 [&>*]:min-w-[180px] max-sm:grid max-sm:w-full max-sm:grid-cols-2 max-sm:gap-3 max-sm:[&>*]:min-w-0">
          {aside}
        </div>
      )}
    </section>
  )
}
