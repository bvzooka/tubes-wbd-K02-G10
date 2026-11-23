export default function Loading() {
  return (
    <p role="status" className="flex items-center justify-center gap-2.5 py-10 text-sm font-semibold text-muted">
      <span
        aria-hidden="true"
        className="h-4 w-4 animate-spin rounded-full border-2 border-green-mid border-t-green motion-reduce:animate-none"
      />
      Memuat…
    </p>
  )
}
