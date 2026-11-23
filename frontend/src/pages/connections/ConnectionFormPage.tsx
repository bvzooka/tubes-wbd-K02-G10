import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { Connection, ConnectionInput, ConnectionTestInput, DBMS, SSLMode, TestResult } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import ErrorMessage from '../../components/ErrorMessage'
import Field from '../../components/Field'
import FormFooter from '../../components/FormFooter'
import FormSection from '../../components/FormSection'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import { dbmsLabel } from '../databases/paths'
import { connectionsKey } from './keys'

const dbmsOptions: DBMS[] = ['postgres', 'mysql', 'mariadb']
const sslModes: SSLMode[] = ['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full']
const defaultPort: Record<DBMS, string> = { postgres: '5432', mysql: '3306', mariadb: '3306' }

// Port disimpan sebagai string selama diketik; dikirim sebagai angka saat submit.
type FormValues = Omit<ConnectionInput, 'port'> & { port: string }

// Nilai awal sama dengan form M1.
const emptyForm: FormValues = {
  name: '',
  dbms: 'postgres',
  host: 'localhost',
  port: defaultPort.postgres,
  database_name: '',
  db_username: '',
  password: '',
  ssl_mode: 'prefer',
}

function fromConnection(c: Connection): FormValues {
  return {
    name: c.name,
    dbms: c.dbms,
    host: c.host,
    port: String(c.port),
    database_name: c.database_name,
    db_username: c.db_username,
    password: '',
    ssl_mode: c.ssl_mode,
  }
}

// toInput mengubah isian form menjadi body API. Port yang bukan angka menjadi 0
// supaya backend yang menolaknya dengan pesan per field (422), bukan dicek dua kali di sini.
function toInput(v: FormValues): ConnectionInput {
  const port = Number(v.port.trim())
  return { ...v, port: Number.isInteger(port) ? port : 0 }
}

// ConnectionFormPage dipakai untuk /admin/connections/new (buat) dan /admin/connections/:id (ubah).
export default function ConnectionFormPage() {
  const { id } = useParams()
  const isNew = id === undefined
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<FormValues>(emptyForm)

  const conn = useQuery({
    queryKey: [...connectionsKey, id],
    queryFn: () => api<Connection>('GET', `/admin/connections/${id}`),
    enabled: !isNew,
  })
  useEffect(() => {
    if (conn.data) setForm(fromConnection(conn.data))
  }, [conn.data])

  const save = useMutation({
    mutationFn: () =>
      isNew
        ? api<Connection>('POST', '/admin/connections', toInput(form))
        : api<Connection>('PUT', `/admin/connections/${id}`, toInput(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: connectionsKey })
      queryClient.invalidateQueries({ queryKey: ['databases'] })
      navigate('/admin/connections')
    },
  })

  // Saat mengedit, id ikut dikirim supaya backend memakai password tersimpan bila isian dikosongkan.
  const test = useMutation({
    mutationFn: () => {
      const body: ConnectionTestInput = { ...toInput(form), ...(isNew ? {} : { id: Number(id) }) }
      return api<TestResult>('POST', '/admin/connections/test', body)
    },
  })

  const setActive = useMutation({
    mutationFn: (active: boolean) =>
      api<Connection>('POST', `/admin/connections/${id}/${active ? 'activate' : 'deactivate'}`),
    onSuccess: (c) => {
      queryClient.setQueryData([...connectionsKey, id], c)
      queryClient.invalidateQueries({ queryKey: connectionsKey, exact: true })
      queryClient.invalidateQueries({ queryKey: ['databases'] })
    },
  })

  if (!isNew && conn.isLoading) return <Loading />
  if (!isNew && !conn.data) return <ErrorMessage error={conn.error} />

  // Error validasi bisa datang dari Simpan maupun Uji koneksi. Keduanya saling me-reset
  // (lihat submit dan runTest), jadi paling banyak satu yang terisi.
  const fieldError = [save.error, test.error].find((e) => e instanceof ApiError && e.status === 422)
  const fields = fieldError instanceof ApiError ? fieldError.fields : {}

  // Hasil uji lama tidak berlaku lagi begitu isian berubah.
  function update<K extends keyof FormValues>(key: K, value: FormValues[K]) {
    setForm((f) => ({ ...f, [key]: value }))
    test.reset()
  }

  // Ganti port ke default DBMS baru hanya bila port masih default DBMS lama (belum diubah manual).
  function changeDBMS(dbms: DBMS) {
    setForm((f) => ({ ...f, dbms, port: f.port === defaultPort[f.dbms] ? defaultPort[dbms] : f.port }))
    test.reset()
  }

  function submit(e: FormEvent) {
    e.preventDefault()
    test.reset()
    save.mutate()
  }

  function runTest() {
    save.reset()
    test.mutate()
  }


  const otherError = [save.error, test.error, setActive.error].find(
    (e) => e && !(e instanceof ApiError && e.status === 422),
  )

  return (
    <>
      <Breadcrumbs
        items={[{ label: 'Koneksi', to: '/admin/connections' }, { label: isNew ? 'Baru' : (conn.data?.name ?? '') }]}
      />
      <PageHeader
        title={isNew ? 'Tambah koneksi' : `Ubah ${conn.data?.name ?? ''}`}
        titleExtra={
          conn.data && (
            <Badge tone={conn.data.is_active ? 'green' : 'neutral'} dot>
              {conn.data.is_active ? 'Aktif' : 'Nonaktif'}
            </Badge>
          )
        }
        subtitle="Isi detail server, lalu uji koneksi sebelum menyimpan."
      />

      <div className="max-w-[860px] space-y-4">
        {test.data && (
          <p role="status" className={'alert ' + (test.data.ok ? 'alert-success' : 'alert-error')}>
            <span className="alert-mark" aria-hidden="true">
              <Icon name={test.data.ok ? 'check' : 'alert'} className="h-[18px] w-[18px]" />
            </span>
            {test.data.message}
          </p>
        )}
        <ErrorMessage error={otherError} />
        {fieldError !== undefined && (
          <p className="alert alert-warning">
            <span className="alert-mark" aria-hidden="true">
              !
            </span>
            Periksa kembali isian yang ditandai.
          </p>
        )}

        <form onSubmit={submit} className="card overflow-hidden p-0" noValidate autoComplete="off">
          <FormSection
            icon="db"
            tone="green"
            title="Identitas"
            desc="Nama yang tampil di daftar dan jenis DBMS-nya."
          >
            <Field label="Nama koneksi" error={fields.name}>
              <input
                className="input"
                value={form.name}
                onChange={(e) => update('name', e.target.value)}
                maxLength={64}
                required
              />
            </Field>
            <Field label="DBMS" error={fields.dbms}>
              <select className="input" value={form.dbms} onChange={(e) => changeDBMS(e.target.value as DBMS)}>
                {dbmsOptions.map((d) => (
                  <option key={d} value={d}>
                    {dbmsLabel[d]}
                  </option>
                ))}
              </select>
            </Field>
          </FormSection>

          <FormSection icon="server" tone="butter" title="Server" desc="Alamat server database target.">
            <Field label="Host" error={fields.host}>
              <input className="input" value={form.host} onChange={(e) => update('host', e.target.value)} required />
            </Field>
            <Field label="Port" error={fields.port} hint="Bawaan: PostgreSQL 5432, MySQL dan MariaDB 3306.">
              <input
                className="input"
                inputMode="numeric"
                value={form.port}
                onChange={(e) => update('port', e.target.value)}
                required
              />
            </Field>
            <Field label="Nama database" error={fields.database_name}>
              <input
                className="input"
                value={form.database_name}
                onChange={(e) => update('database_name', e.target.value)}
                required
              />
            </Field>
            <Field label="SSL mode" error={fields.ssl_mode}>
              <select
                className="input"
                value={form.ssl_mode}
                onChange={(e) => update('ssl_mode', e.target.value as SSLMode)}
              >
                {sslModes.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </Field>
          </FormSection>

          <FormSection
            icon="key"
            tone="ink"
            title="Kredensial"
            desc="Password dienkripsi sebelum disimpan dan tidak pernah ditampilkan kembali."
          >
            <Field label="Username database" error={fields.db_username}>
              <input
                className="input"
                value={form.db_username}
                onChange={(e) => update('db_username', e.target.value)}
                autoComplete="off"
                required
              />
            </Field>
            <Field
              label="Password database"
              error={fields.password}
              hint={isNew ? undefined : 'Kosongkan jika password tidak ingin diubah.'}
            >
              <input
                className="input"
                type="password"
                value={form.password}
                onChange={(e) => update('password', e.target.value)}
                autoComplete="new-password"
                required={isNew}
              />
            </Field>
          </FormSection>

          <FormFooter>
            <button type="submit" className="btn-primary" disabled={save.isPending}>
              <Icon name="check" />
              {save.isPending ? 'Menyimpan…' : 'Simpan'}
            </button>
            <button type="button" className="btn-secondary" disabled={test.isPending} onClick={runTest}>
              <Icon name="bolt" />
              {test.isPending ? 'Menguji…' : 'Uji koneksi'}
            </button>
            <Link to="/admin/connections" className="btn-secondary">
              Batal
            </Link>
            {conn.data && (
              <button
                type="button"
                className={(conn.data.is_active ? 'btn-danger' : 'btn-dark') + ' sm:ml-auto'}
                disabled={setActive.isPending}
                onClick={() => setActive.mutate(!conn.data.is_active)}
              >
                {conn.data.is_active ? 'Nonaktifkan' : 'Aktifkan'}
              </button>
            )}
          </FormFooter>
        </form>
      </div>
    </>
  )
}
