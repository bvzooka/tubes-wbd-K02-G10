import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

export const metrics = {
  // durasi dari menekan run sampai hasil akhirnya terlihat oleh user
  completion: new Trend('job_completion', true),
  // dari masuk antrean sampai diambil worker, termasuk menunggu kunci database
  queueWait: new Trend('job_queue_wait', true),
  // dari mulai dijalankan worker sampai selesai
  runDuration: new Trend('job_run_duration', true),
  success: new Rate('job_success'),
  finished: new Counter('jobs_finished'),
  // baris yang di-commit migration batch; mode normal tidak melaporkan jumlah baris
  rows: new Counter('migration_rows'),
  // skenario database yang sama: permintaan yang kalah duluan dan ditolak 409
  conflicts: new Counter('request_conflicts'),
};

const POLL_INTERVAL = 0.25; 
const JOB_TIMEOUT = Number(__ENV.JOB_TIMEOUT || 300) * 1000;

// mengembalikan versi ready terkecil, atau null kalau persediaan habis
export function nextReadyVersion(client, connID) {
  const res = client.get(`/databases/${connID}/migrations`, { name: 'GET /databases/{id}/migrations' });
  const ready = (res.data?.items ?? []).filter((m) => m.state === 'ready').map((m) => m.version);
  return ready.length ? Math.min(...ready) : null;
}

// mengajukan apply. Dengan allowConflict, 409 adalah hasil yang wajar: VU lain sudah lebih dulu mengajukan versi yang sama.
export function requestApply(client, connID, version, { allowConflict = false } = {}) {
  const res = client.post(`/databases/${connID}/migrations/${version}/executions`, { kind: 'apply' }, {
    name: 'POST /databases/{id}/migrations/{v}/executions',
    expect: allowConflict ? [201, 409] : [201],
  });
  if (res.status === 409 && allowConflict) {
    metrics.conflicts.add(1);
    check(res, { 'ditolak sebagai konflik, bukan error lain': (r) => r.error?.code === 'conflict' });
    return null;
  }
  check(res, { 'permintaan apply diterima dan langsung disetujui': (r) => r.status === 201 && r.data.status === 'approved' });
  return res.status === 201 ? res.data : null;
}

// menekan run dua kali: tombol yang ditekan berulang harus mendapat job yang sama
export function run(client, executionID) {
  const opts = { name: 'POST /executions/{id}/run' };
  const first = client.post(`/executions/${executionID}/run`, null, opts);
  const again = client.post(`/executions/${executionID}/run`, null, opts);
  check(first, { 'run menjawab 202 dengan job ID': (r) => r.status === 202 && !!r.data?.job_id });
  check(again, { 'run berulang mengembalikan job ID yang sama': (r) => r.data?.job_id === first.data?.job_id });
  return first.data?.job_id ?? null;
}

// memantau job sampai selesai lalu mencatat metrik dari data eksekusi, submittedAt adalah waktu sebelum run ditekan.
export function waitForResult(client, jobID, executionID, submittedAt, tags) {
  while (Date.now() - submittedAt < JOB_TIMEOUT) {
    const job = client.get(`/jobs/${jobID}`, { name: 'GET /jobs/{id}' }).data;
    if (job?.state === 'done') {
      metrics.completion.add(Date.now() - submittedAt, tags);
      return record(client, executionID, tags);
    }
    sleep(POLL_INTERVAL);
  }
  metrics.success.add(false, tags);
  check(null, { 'job selesai sebelum batas waktu': () => false });
  return null;
}

function record(client, executionID, tags) {
  const e = client.get(`/executions/${executionID}`, { name: 'GET /executions/{id}' }).data.execution;
  const ok = e.status === 'success';
  metrics.success.add(ok, tags);
  metrics.finished.add(1, tags);
  check(e, { 'migration berhasil (status eksekusi success)': () => ok });
  if (e.queued_at && e.started_at) {
    metrics.queueWait.add(Date.parse(e.started_at) - Date.parse(e.queued_at), tags);
  }
  if (e.started_at && e.finished_at) {
    metrics.runDuration.add(Date.parse(e.finished_at) - Date.parse(e.started_at), tags);
  }
  metrics.rows.add(e.rows_committed, tags);
  return e;
}
