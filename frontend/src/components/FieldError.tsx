interface FieldErrorProps {
  message?: string
}

export default function FieldError({ message }: FieldErrorProps) {
  if (!message) return null
  return <span className="mt-1 block text-xs text-red-600">{message}</span>
}
