interface EmptyStateProps {
  message: string
}

export default function EmptyState({ message }: EmptyStateProps) {
  return <p className="py-8 text-center text-sm text-slate-500">{message}</p>
}
