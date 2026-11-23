interface FieldErrorProps {
  message?: string
}

export default function FieldError({ message }: FieldErrorProps) {
  if (!message) return null
  return <span className="field-error">{message}</span>
}
