import type { ReactNode } from 'react'

interface FormFooterProps {
  children: ReactNode
}

// FormFooter adalah kaki form berlatar lembut tempat tombol Simpan, Batal, dan aksi lain.
export default function FormFooter({ children }: FormFooterProps) {
  return <div className="flex flex-wrap gap-2.5 bg-paper-soft px-[1.6rem] py-5 max-md:px-5">{children}</div>
}
