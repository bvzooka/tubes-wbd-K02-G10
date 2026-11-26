import http from 'k6/http';

export const BASE_URL = (__ENV.BASE_URL || 'http://server:8080').replace(/\/$/, '');
const API = `${BASE_URL}/api/v1`;

const SUCCESS = http.expectedStatuses(200, 201, 202, 204);

export class Client {
  constructor() {
    this.csrf = '';
  }

  login(username, password) {
    this.csrf = this.request('GET', '/auth/csrf').data.csrf_token;
    const res = this.request('POST', '/auth/login', { username, password });
    if (res.status !== 200) {
      throw new Error(`login ${username} gagal: status ${res.status}`);
    }
    this.csrf = res.data.csrf_token;
  }

  get(path, opts) {
    return this.request('GET', path, null, opts);
  }

  post(path, body, opts) {
    return this.request('POST', path, body ?? {}, opts);
  }

  put(path, body, opts) {
    return this.request('PUT', path, body ?? {}, opts);
  }

  request(method, path, body, opts = {}) {
    const headers = { Origin: BASE_URL };
    if (body !== null && body !== undefined) headers['Content-Type'] = 'application/json';
    if (method !== 'GET' && this.csrf) headers['X-CSRF-Token'] = this.csrf;

    const res = http.request(method, API + path, body ? JSON.stringify(body) : null, {
      headers,
      tags: { name: opts.name || path, ...opts.tags },
      responseCallback: opts.expect ? http.expectedStatuses(...opts.expect) : SUCCESS,
    });
    const envelope = parseJSON(res.body);
    return { status: res.status, data: envelope?.data, error: envelope?.error };
  }
}

function parseJSON(body) {
  if (!body) return null;
  try {
    return JSON.parse(body);
  } catch {
    return null;
  }
}
