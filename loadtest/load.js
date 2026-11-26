import { check, sleep } from 'k6';
import exec from 'k6/execution';

import { Client } from './lib/client.js';
import { nextReadyVersion, requestApply, run, waitForResult } from './lib/jobs.js';
import { prepare } from './lib/prepare.js';

const USERNAME = __ENV.ADMIN_USERNAME || 'admin';
const PASSWORD = __ENV.ADMIN_PASSWORD;

const STAGE_SECONDS = Number(__ENV.STAGE_SECONDS || 30);
const VUS = {
  normal: Number(__ENV.NORMAL_VUS || 5),
  high: Number(__ENV.HIGH_VUS || 15),
  peak: Number(__ENV.PEAK_VUS || 30),
  sameDatabase: Number(__ENV.SAME_DB_VUS || 4),
};
const JOB_PAUSE = Number(__ENV.JOB_PAUSE || 3); // detik antar job per VU
const CONFLICT_RETRY = 0.5; // detik sebelum VU yang kalah mencoba versi berikutnya
const EVENTS_ROWS = 3_000_000; // isi public.events dari make seed-large
const PAGE_SIZE = 50;

const STAGES = [
  { label: 'normal', target: VUS.normal }, // naik ke beban normal
  { label: 'normal', target: VUS.normal }, // tahan
  { label: 'high', target: VUS.high }, // naik di atas normal
  { label: 'high', target: VUS.high }, // tahan
  { label: 'peak', target: VUS.peak }, // puncak
  { label: 'ramp_down', target: VUS.normal }, // turun lagi
  { label: 'recovery', target: VUS.normal }, // pemulihan setelah beban kembali normal
];
const TOTAL_SECONDS = STAGES.length * STAGE_SECONDS;
const LOAD_LEVELS = ['normal', 'high', 'peak', 'ramp_down', 'recovery'];
const RECOVERED_LEVELS = ['normal', 'recovery']; // tingkat yang wajib memenuhi batas latensi

// setiap VU job menyelesaikan paling banyak satu job per JOB_PAUSE detik
const MIGRATIONS_PER_DATABASE = Math.ceil(TOTAL_SECONDS / JOB_PAUSE) + 5;

// satu skenario per database berbeda, masing-masing dengan satu VU
const distinctScenarios = Object.fromEntries([0, 1, 2, 3].map((i) => [`jobs_db_${i + 1}`, {
  executor: 'constant-vus',
  exec: 'migrateDistinct',
  vus: 1,
  duration: `${TOTAL_SECONDS}s`,
  gracefulStop: '3m', // job yg sedang berjalan ditunggu sampai selesai
  env: { DB_INDEX: String(i) },
  tags: { flow: 'distinct_db' },
}]));

export const options = {
  setupTimeout: '10m',
  // k6 mengosongkan cookie di setiap iterasi, sesi login VU harus tetap dipakai
  noCookiesReset: true,
  scenarios: {
    api_read: {
      executor: 'ramping-vus',
      exec: 'readData',
      startVUs: 0,
      stages: STAGES.map((s) => ({ duration: `${STAGE_SECONDS}s`, target: s.target })),
      gracefulRampDown: '10s',
      tags: { flow: 'api_read' },
    },
    ...distinctScenarios,
    jobs_same_db: {
      executor: 'constant-vus',
      exec: 'migrateSameDatabase',
      vus: VUS.sameDatabase,
      duration: `${TOTAL_SECONDS}s`,
      gracefulStop: '3m',
      tags: { flow: 'same_db' },
    },
  },
  thresholds: thresholds(),
};

// threshold lulus: tidak ada error di tingkat beban mana pun, dan p95 baca di bawah 2 detik pada beban normal dan setelah pulih. 
// Satu halaman acak dari 3 juta baris memakai OFFSET dalam,
// mediannya sekitar 0,4 detik walau tanpa beban, jadi 2 detik memberi ruang sekitar 5x.
function thresholds() {
  const t = {
    'http_req_failed{flow:api_read}': ['rate<0.01'],
    // 409 pada database yang sama sudah dinyatakan sebagai status yang diharapkan
    'http_req_failed{flow:same_db}': ['rate<0.01'],
    'http_req_failed{flow:distinct_db}': ['rate<0.01'],
    job_success: ['rate>0.99'],
    checks: ['rate>0.99'],
  };
  for (const level of LOAD_LEVELS) {
    t[`http_req_duration{flow:api_read,load:${level}}`] = [RECOVERED_LEVELS.includes(level) ? 'p(95)<2000' : 'p(95)>=0'];
    t[`http_reqs{flow:api_read,load:${level}}`] = ['count>=0'];
    t[`http_req_failed{flow:api_read,load:${level}}`] = ['rate>=0'];
    t[`job_completion{load:${level}}`] = ['p(95)>=0'];
    t[`job_queue_wait{load:${level}}`] = ['p(95)>=0'];
    t[`jobs_finished{load:${level}}`] = ['count>=0'];
  }
  for (const flow of ['distinct_db', 'same_db']) {
    t[`job_completion{flow:${flow}}`] = ['p(95)>=0'];
    t[`job_queue_wait{flow:${flow}}`] = ['p(95)>=0'];
    t[`job_run_duration{flow:${flow}}`] = ['p(95)>=0'];
    t[`jobs_finished{flow:${flow}}`] = ['count>=0'];
  }
  for (const mode of ['normal', 'batch']) {
    t[`job_run_duration{mode:${mode}}`] = ['p(95)>=0'];
  }
  return t;
}

export function setup() {
  if (!PASSWORD) throw new Error('ADMIN_PASSWORD wajib diisi');
  const targets = prepare({ username: USERNAME, password: PASSWORD, migrationsPerDatabase: MIGRATIONS_PER_DATABASE });
  const admin = new Client();
  admin.login(USERNAME, PASSWORD);
  // nilai awal counter dipakai teardown untuk memeriksa isi tabel setelah semua migration
  const baseline = Object.fromEntries([targets.shared, ...targets.distinct].map((id) => [id, firstCounter(admin, id)]));
  return { ...targets, baseline };
}


export function readData(data) {
  const client = session();
  const tags = { load: loadLevel() };
  const page = 1 + Math.floor(Math.random() * (EVENTS_ROWS / PAGE_SIZE));
  const res = client.get(`/databases/${data.read}/tables/public/events/data?page=${page}&size=${PAGE_SIZE}`, {
    name: 'GET /databases/{id}/tables/events/data', tags,
  });
  check(res, { 'halaman events terbaca': (r) => r.status === 200 && r.data.rows.length === PAGE_SIZE });
  sleep(0.5);
}

export function migrateDistinct(data) {
  migrateNext(data.distinct[Number(__ENV.DB_INDEX)], { allowConflict: false });
}

export function migrateSameDatabase(data) {
  migrateNext(data.shared, { allowConflict: true });
}

function migrateNext(connID, { allowConflict }) {
  const client = session();
  const load = loadLevel();
  const version = nextReadyVersion(client, connID);
  if (version === null) {
    sleep(JOB_PAUSE); // persediaan migration habis
    return;
  }
  const execution = requestApply(client, connID, version, { allowConflict });
  if (!execution) {
    sleep(CONFLICT_RETRY);
    return;
  }
  const submittedAt = Date.now();
  const jobID = run(client, execution.id);
  if (jobID) {
    waitForResult(client, jobID, execution.id, submittedAt, { load, mode: execution.mode });
  }
  sleep(JOB_PAUSE);
}


export function teardown(data) {
  const admin = new Client();
  admin.login(USERNAME, PASSWORD);

  // database yang sama: setiap versi hanya boleh punya satu eksekusi, yang lain harus ditolak 409
  const sharedRuns = historySince(admin, data.shared, data.firstVersion[data.shared]);
  check(sharedRuns, { 'database yang sama: satu eksekusi per versi': (h) => everyVersionOnce(h) });

  // isi tabel harus naik tepat sebanyak migration yang berhasil di database itu
  for (const id of [data.shared, ...data.distinct]) {
    const applied = historySince(admin, id, data.firstVersion[id]).filter((h) => h.status === 'success').length;
    check(firstCounter(admin, id), { 'counter tabel = nilai awal + jumlah migration berhasil': (c) => c === data.baseline[id] + applied });
  }
}

// ---- pembantu ----

// setiap VU login sekali lalu memakai sesinya sampai selesai
let client = null;
function session() {
  if (!client) {
    client = new Client();
    client.login(USERNAME, PASSWORD);
  }
  return client;
}

function loadLevel() {
  const elapsed = (Date.now() - exec.scenario.startTime) / 1000;
  return STAGES[Math.min(Math.floor(elapsed / STAGE_SECONDS), STAGES.length - 1)].label;
}

function firstCounter(admin, connID) {
  const res = admin.get(`/databases/${connID}/tables/public/k6_items/data?page=1&size=1`, {
    name: 'GET /databases/{id}/tables/k6_items/data',
  });
  const counterColumn = res.data.columns.indexOf('counter');
  return Number(res.data.rows[0][counterColumn]);
}

// riwayat eksekusi untuk migration yang dibuat di pengujian ini, satu entri per percobaan
function historySince(admin, connID, firstVersion) {
  const entries = [];
  for (let page = 1; ; page++) {
    const res = admin.get(`/databases/${connID}/executions?page=${page}`, { name: 'GET /databases/{id}/executions' });
    entries.push(...res.data.items.filter((h) => h.version >= firstVersion));
    if (!res.data.has_next) return entries;
  }
}

function everyVersionOnce(entries) {
  const executions = {};
  for (const h of entries) {
    executions[h.version] = executions[h.version] ?? new Set();
    executions[h.version].add(h.execution_id);
  }
  return Object.values(executions).every((ids) => ids.size === 1);
}
