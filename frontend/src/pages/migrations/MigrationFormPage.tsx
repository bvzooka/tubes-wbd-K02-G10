import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { Migration, MigrationDetail, MigrationInput, MigrationMode } from '../../api/types'
import Breadcrumbs from '../../components/Breadcrumbs'
import ErrorMessage from '../../components/ErrorMessage'
import Field from '../../components/Field'
import FormFooter from '../../components/FormFooter'
import FormSection from '../../components/FormSection'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import { migrationsKey, migrationsPath } from './paths'

// angka disimpan sebagai string selama diketik
interface FormValues {
  name: string
  mode: MigrationMode
  sql_up: string
  sql_down: string
  irreversible: boolean
  irreversible_reason: string
  schema: string
  table: string
  key_column: string
  batch_size: string
}

const emptyForm: FormValues = {
  name: '',
  mode: 'normal',
  sql_up: '',
  sql_down: '',
  irreversible: false,
  irreversible_reason: '',
  schema: 'public',
  table: '',
  key_column: 'id',
  batch_size: '10000',
}

function fromMigration(m: Migration): FormValues {
  return {
    name: m.name,
    mode: m.mode,
    sql_up: m.sql_up,
    sql_down: m.sql_down,
    irreversible: m.irreversible,
    irreversible_reason: m.irreversible_reason,
    schema: m.batch?.schema ?? emptyForm.schema,
    table: m.batch?.table ?? '',
    key_column: m.batch?.key_column ?? emptyForm.key_column,
    batch_size: String(m.batch?.batch_size ?? emptyForm.batch_size),
  }
}

// nama dipotong per karakter supaya tidak melebihi batas isian
function copyOf(m: Migration): FormValues {
  const name = Array.from(`Salinan versi ${m.version}: ${m.name}`).slice(0, maxNameChars).join('')
  return { ...fromMigration(m), name }
}

// validasi tidak diulang di sini, backend yang menjawab pesan per field
function toInput(v: FormValues): MigrationInput {
  const size = Number(v.batch_size.trim())
  return {
    name: v.name,
    mode: v.mode,
    sql_up: v.sql_up,
    sql_down: v.sql_down,
    irreversible: v.irreversible,
    irreversible_reason: v.irreversible_reason,
    batch:
      v.mode === 'batch'
        ? {
            schema: v.schema,
            table: v.table,
            key_column: v.key_column,
            // isian bukan angka dikirim -1 supaya ditolak, karena 0 berarti pakai ukuran bawaan
            batch_size: Number.isInteger(size) ? size : -1,
          }
        : null,
  }
}

const sqlClass = 'input min-h-[180px] font-mono text-[0.85rem]'
const maxNameChars = 120

// dipakai untuk membuat draft baru, menyalin versi lama, dan mengubah draft yang sudah ada
export default function MigrationFormPage() {
  const { id = '', version } = useParams()
  const isNew = version === undefined
  const [params] = useSearchParams()
  const copyFrom = isNew ? params.get('from') || null : null
  // versi yang isinya perlu dimuat, kosong untuk draft yang benar-benar baru
  const loadVersion = version ?? copyFrom
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<FormValues>(emptyForm)
  // revisi yang sedang disunting, dikirim balik supaya perubahan orang lain tidak tertimpa
  const [revision, setRevision] = useState(0)

  const detail = useQuery({
    queryKey: [...migrationsKey(id), loadVersion],
    queryFn: () => api<MigrationDetail>('GET', `/databases/${id}/migrations/${loadVersion}`),
    enabled: loadVersion !== null,
    // isian yang sedang diketik tidak boleh diganti otomatis saat tab difokuskan lagi
    refetchOnWindowFocus: false,
  })
  useEffect(() => {
    if (!detail.data) return
    const m = detail.data.migration
    setForm(isNew ? copyOf(m) : fromMigration(m))
    setRevision(m.revision)
  }, [detail.data, isNew])

  const save = useMutation({
    mutationFn: () =>
      isNew
        ? api<Migration>('POST', `/databases/${id}/migrations`, toInput(form))
        : api<Migration>('PUT', `/databases/${id}/migrations/${version}`, { ...toInput(form), revision }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: migrationsKey(id) })
      navigate(migrationsPath(id))
    },
  })

  if (loadVersion !== null && detail.isLoading) return <Loading />
  if (loadVersion !== null && !detail.data) return <ErrorMessage error={detail.error} />

  const invalid = save.error instanceof ApiError && save.error.status === 422 ? save.error : null
  const fields = invalid?.fields ?? {}
  // salinan boleh disimpan walau versi asalnya sudah terkunci
  const locked = !isNew && detail.data !== undefined && detail.data.migration.state !== 'draft'

  function update<K extends keyof FormValues>(key: K, value: FormValues[K]) {
    setForm((f) => ({ ...f, [key]: value }))
  }

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate()
  }

  return (
    <>
      <Breadcrumbs
        items={[
          { label: 'Database', to: '/databases' },
          { label: 'Migration', to: migrationsPath(id) },
          { label: isNew ? 'Baru' : `v${version}` },
        ]}
      />
      <PageHeader
        title={isNew ? 'Buat migration' : `Ubah versi ${version}`}
        subtitle={
          copyFrom
            ? `Isi disalin dari versi ${copyFrom} dan baru menjadi versi baru setelah disimpan.`
            : 'Draft boleh disimpan berkali-kali. SQL baru terkunci setelah dibekukan.'
        }
      />

      <div className="max-w-[860px] space-y-4">
        {locked && (
          <p className="alert alert-warning">
            <span className="alert-mark" aria-hidden="true">
              !
            </span>
            Migration ini bukan draft lagi, jadi tidak bisa diubah.
          </p>
        )}
        {invalid ? (
          <p role="alert" className="alert alert-warning">
            <span className="alert-mark" aria-hidden="true">
              !
            </span>
            {invalid.message}
          </p>
        ) : (
          <ErrorMessage error={save.error} />
        )}

        <form onSubmit={submit} className="card overflow-hidden p-0" noValidate autoComplete="off">
          <FormSection icon="history" tone="green" title="Identitas">
            <Field label="Nama" error={fields.name}>
              <input className="input" value={form.name} maxLength={120} onChange={(e) => update('name', e.target.value)} />
            </Field>
            <Field label="Mode" error={fields.mode} hint="Batch memotong perubahan data besar menjadi bagian kecil.">
              <select className="input" value={form.mode} onChange={(e) => update('mode', e.target.value as MigrationMode)}>
                <option value="normal">Normal</option>
                <option value="batch">Batch</option>
              </select>
            </Field>
          </FormSection>

          {form.mode === 'batch' && (
            <FormSection
              icon="layers"
              tone="butter"
              title="Pengaturan batch"
              desc="Kolom kunci harus integer dan primary key atau unique, dan nilainya tidak berubah."
            >
              <Field label="Schema" error={fields['batch.schema']}>
                <input className="input" value={form.schema} onChange={(e) => update('schema', e.target.value)} />
              </Field>
              <Field label="Tabel" error={fields['batch.table']}>
                <input className="input" value={form.table} onChange={(e) => update('table', e.target.value)} />
              </Field>
              <Field label="Kolom kunci" error={fields['batch.key_column']}>
                <input className="input" value={form.key_column} onChange={(e) => update('key_column', e.target.value)} />
              </Field>
              <Field label="Ukuran batch" error={fields['batch.batch_size']} hint="1 sampai 100000 baris.">
                <input
                  className="input"
                  inputMode="numeric"
                  value={form.batch_size}
                  onChange={(e) => update('batch_size', e.target.value)}
                />
              </Field>
            </FormSection>
          )}

          <FormSection
            icon="bolt"
            tone="ink"
            title="SQL"
            columns={1}
            desc={
              form.mode === 'batch'
                ? 'Satu statement UPDATE, INSERT, atau DELETE. Pakai $1 sebagai batas bawah (tidak ikut) dan $2 sebagai batas atas (ikut).'
                : 'Seluruh SQL dijalankan dalam satu transaction, jadi tidak perlu BEGIN atau COMMIT.'
            }
          >
            <Field label="SQL up" error={fields.sql_up} hint="Perubahan yang diterapkan.">
              <textarea className={sqlClass} spellCheck={false} value={form.sql_up} onChange={(e) => update('sql_up', e.target.value)} />
            </Field>
            <label className="check">
              <input type="checkbox" checked={form.irreversible} onChange={(e) => update('irreversible', e.target.checked)} />
              Irreversible, perubahan ini tidak bisa dibatalkan
            </label>
            {form.irreversible ? (
              <Field label="Alasan irreversible" error={fields.irreversible_reason}>
                <textarea
                  className="input"
                  value={form.irreversible_reason}
                  onChange={(e) => update('irreversible_reason', e.target.value)}
                />
              </Field>
            ) : (
              <Field label="SQL down" error={fields.sql_down} hint="Perintah kebalikan untuk rollback. Wajib sebelum dibekukan.">
                <textarea className={sqlClass} spellCheck={false} value={form.sql_down} onChange={(e) => update('sql_down', e.target.value)} />
              </Field>
            )}
          </FormSection>

          <FormFooter>
            <button type="submit" className="btn-primary" disabled={save.isPending || locked}>
              <Icon name="check" />
              {save.isPending ? 'Menyimpan…' : 'Simpan draft'}
            </button>
            <Link to={migrationsPath(id)} className="btn-secondary">
              Batal
            </Link>
          </FormFooter>
        </form>
      </div>
    </>
  )
}
