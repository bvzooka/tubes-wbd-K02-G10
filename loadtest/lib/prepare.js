import { Client } from './client.js';

const GROUP_NAME = 'k6-loadtest';

// nama database harus sama dgn loadtest/targets.sql
export const TARGETS = {
  read: { name: 'k6-sampledb', database: 'sampledb' },
  shared: { name: 'k6-shared', database: 'k6_shared' },
  distinct: [1, 2, 3, 4].map((n) => ({ name: `k6-db-${n}`, database: `k6_db_${n}` })),
};

// host dan port database target dilihat dari server, bukan dari k6
const TARGET_HOST = __ENV.TARGET_HOST || 'targetdb';
const TARGET_PORT = Number(__ENV.TARGET_PORT || 5432);

export function prepare({ username, password, migrationsPerDatabase }) {
  const admin = new Client();
  admin.login(username, password);

  const read = ensureConnection(admin, TARGETS.read);
  const shared = ensureConnection(admin, TARGETS.shared);
  const distinct = TARGETS.distinct.map((t) => ensureConnection(admin, t));
  const migrating = [shared, ...distinct];

  ensureGroup(admin, [read, ...migrating]);
  requireLargeDataset(admin, read);

  // versi pertama yang dibuat di pengujian ini, dipakai teardown utk memisahkan dari riwayat lama
  const firstVersion = {};
  for (const connID of migrating) {
    clearLeftovers(admin, connID);
    firstVersion[connID] = createMigrations(admin, connID, migrationsPerDatabase);
  }
  return { read, shared, distinct, firstVersion };
}

function ensureConnection(admin, target) {
  const body = {
    name: target.name,
    dbms: 'postgres',
    host: TARGET_HOST,
    port: TARGET_PORT,
    database_name: target.database,
    db_username: 'postgres',
    password: 'postgres',
    ssl_mode: 'disable',
  };
  const existing = admin.get('/admin/connections').data.find((c) => c.name === target.name);
  if (!existing) {
    return must(admin.post('/admin/connections', body), `buat koneksi ${target.name}`).id;
  }
  if (existing.host !== TARGET_HOST || existing.port !== TARGET_PORT) {
    must(admin.put(`/admin/connections/${existing.id}`, body, { name: 'PUT /admin/connections/{id}' }),
      `perbarui koneksi ${target.name}`);
  }
  if (!existing.is_active) {
    must(admin.post(`/admin/connections/${existing.id}/activate`, null, { name: 'POST /admin/connections/{id}/activate' }),
      `aktifkan koneksi ${target.name}`);
  }
  return existing.id;
}

function ensureGroup(admin, connectionIDs) {
  const me = admin.get('/auth/me').data;
  const userID = (me.user ?? me).id;

  let group = admin.get('/admin/groups').data.find((g) => g.name === GROUP_NAME);
  if (!group) {
    group = must(admin.post('/admin/groups', { name: GROUP_NAME, description: 'akses untuk uji beban k6' }),
      'buat group');
  }
  const detail = admin.get(`/admin/groups/${group.id}`, { name: 'GET /admin/groups/{id}' }).data;
  const member = detail.members.find((m) => m.user_id === userID);
  if (!member) {
    must(admin.post(`/admin/groups/${group.id}/members`, { user_id: userID }, { name: 'POST /admin/groups/{id}/members' }),
      'tambah admin ke group');
  }
  if (!member?.is_leader) {
    must(admin.put(`/admin/groups/${group.id}/leader`, { user_id: userID }, { name: 'PUT /admin/groups/{id}/leader' }),
      'jadikan admin leader');
  }
  for (const connID of connectionIDs) {
    must(admin.put(`/admin/groups/${group.id}/access/${connID}`, { access_level: 'view_edit' },
      { name: 'PUT /admin/groups/{id}/access/{dbID}' }), `beri akses koneksi ${connID}`);
  }
}

function requireLargeDataset(admin, connID) {
  const res = admin.get(`/databases/${connID}/tables/public/events`, { name: 'GET /databases/{id}/tables/{t}', expect: [200, 404] });
  if (res.status !== 200) {
    throw new Error('tabel public.events tidak ada di sampledb: jalankan make seed-large dulu');
  }
}

// yang belum pernah dijalankan dibatalkan, yang gagal di-supersede.
function clearLeftovers(admin, connID) {
  const items = admin.get(`/databases/${connID}/migrations`, { name: 'GET /databases/{id}/migrations' }).data.items;
  for (const m of items) {
    const path = `/databases/${connID}/migrations/${m.version}`;
    const cancelDraft = () =>
      must(admin.post(`${path}/cancel`, null, { name: 'POST /databases/{id}/migrations/{v}/cancel' }), `batalkan v${m.version}`);
    switch (m.state) {
      case 'ready':
        // hanya draft yang bisa dibatalkan, jadi migration ready dibuka lagi dulu
        cancelOpenExecutions(admin, connID, m.version);
        must(admin.post(`${path}/reopen`, null, { name: 'POST /databases/{id}/migrations/{v}/reopen' }), `reopen v${m.version}`);
        cancelDraft();
        break;
      case 'draft':
        cancelDraft();
        break;
      case 'failed':
      case 'rolled_back':
        must(admin.post(`${path}/supersede`, null, { name: 'POST /databases/{id}/migrations/{v}/supersede' }), `supersede v${m.version}`);
        break;
      case 'partial':
      case 'partially_reverted':
      case 'unknown':
        throw new Error(`koneksi ${connID} versi ${m.version} berstatus ${m.state}: pulihkan dulu lewat aplikasi`);
    }
  }
}

function cancelOpenExecutions(admin, connID, version) {
  const detail = admin.get(`/databases/${connID}/migrations/${version}`, { name: 'GET /databases/{id}/migrations/{v}' }).data;
  for (const e of detail.executions) {
    if (e.status === 'queued' || e.status === 'running') {
      throw new Error(`koneksi ${connID} versi ${version} masih dijalankan worker: tunggu sampai selesai`);
    }
    if (e.status === 'pending_review' || e.status === 'approved') {
      must(admin.post(`/executions/${e.id}/cancel`, null, { name: 'POST /executions/{id}/cancel' }), `batalkan eksekusi ${e.id}`);
    }
  }
}

// versi ganjil memakai mode normal, versi genap mode batch, supaya kedua jalur runner diuji.
// tiap migration menaikkan counter semua baris k6_items, dan down-nya menurunkan lagi.
function createMigrations(admin, connID, count) {
  let first = null;
  for (let i = 0; i < count; i++) {
    const draft = i % 2 === 0 ? normalDraft() : batchDraft();
    const created = must(admin.post(`/databases/${connID}/migrations`, draft, { name: 'POST /databases/{id}/migrations' }),
      'buat migration');
    first = first ?? created.version;
    must(admin.post(`/databases/${connID}/migrations/${created.version}/freeze`, { revision: created.revision },
      { name: 'POST /databases/{id}/migrations/{v}/freeze' }), `bekukan v${created.version}`);
  }
  return first;
}

function normalDraft() {
  return {
    name: 'k6 normal: counter + 1',
    mode: 'normal',
    sql_up: 'UPDATE public.k6_items SET counter = counter + 1, updated_at = now();',
    sql_down: 'UPDATE public.k6_items SET counter = counter - 1, updated_at = now();',
  };
}

function batchDraft() {
  return {
    name: 'k6 batch: counter + 1',
    mode: 'batch',
    batch: { schema: 'public', table: 'k6_items', key_column: 'id', batch_size: 50000 },
    sql_up: 'UPDATE public.k6_items SET counter = counter + 1, updated_at = now() WHERE id > $1 AND id <= $2',
    sql_down: 'UPDATE public.k6_items SET counter = counter - 1, updated_at = now() WHERE id > $1 AND id <= $2',
  };
}

// menghentikan setup dengan pesan yang jelas, karena data yang setengah siap membuat hasil uji beban tidak bisa dipercaya
function must(res, what) {
  if (res.status >= 300) {
    throw new Error(`${what} gagal: status ${res.status} ${JSON.stringify(res.error)}`);
  }
  return res.data;
}
