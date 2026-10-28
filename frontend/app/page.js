'use client';

import { useEffect, useState } from 'react';
import { signIn } from './keycloak';

const apiURL = 'http://localhost:8080';
const statuses = {
  submitted: 'Принята', processing: 'Обрабатывается', pending_manager: 'У руководителя',
  auto_approved: 'Одобрена автоматически', approved: 'Одобрена', rejected: 'Отклонена', needs_hr_review: 'Передана в HR',
};

export default function Home() {
  const [keycloak, setKeycloak] = useState(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    signIn().then(value => { if (active) setKeycloak(value); })
      .catch(() => { if (active) setError('Ошибка входа'); });
    return () => { active = false; };
  }, []);
  if (!keycloak) return <p>{error || 'Вход через Keycloak…'}</p>;
  return <Vacations keycloak={keycloak}/>;
}

function Vacations({ keycloak }) {
  const employee = keycloak.hasRealmRole('employee');
  const manager = keycloak.hasRealmRole('manager');
  const [me, setMe] = useState(null);
  const [requests, setRequests] = useState([]);
  const [form, setForm] = useState({ start_date: '', end_date: '', comment: '' });
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [sending, setSending] = useState(false);
  const [offset, setOffset] = useState(0);
  const limit = 20;
  const today = new Date().toISOString().slice(0, 10);

  async function api(path, options = {}) {
    try { await keycloak.updateToken(30); }
    catch { await keycloak.login(); throw new Error('Войдите снова'); }
    const response = await fetch(apiURL + path, {
      ...options, headers: { ...options.headers, Authorization: `Bearer ${keycloak.token}` },
    });
    const body = await response.json();
    if (!response.ok) throw new Error(body.error || `Ошибка ${response.status}`);
    return body;
  }

  useEffect(() => {
    let active = true;
    let running = false;
    async function refresh() {
      if (running) return;
      running = true;
      try {
        const [list, profile] = await Promise.all([
          api(`${manager ? '/api/manager' : '/api'}/vacation-requests?limit=${limit}&offset=${offset}`),
          employee ? api('/api/me') : Promise.resolve(null),
        ]);
        if (active) { setRequests(list); setMe(profile); }
      } catch (e) { if (active) setError(e.message); }
      finally { running = false; }
    }
    refresh();
    const timer = setInterval(refresh, 3000);
    return () => { active = false; clearInterval(timer); };
  }, [offset, keycloak, manager, employee]);

  function change(e) {
    setForm({ ...form, [e.target.name]: e.target.value });
    setKey(crypto.randomUUID()); setMessage('');
  }

  async function submit(e) {
    e.preventDefault(); setSending(true); setError(''); setMessage('');
    try {
      await api('/api/vacation-requests', {
        method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify(form),
      });
      setForm({ start_date: '', end_date: '', comment: '' }); setKey(crypto.randomUUID()); setOffset(0);
      setMessage('Заявка принята, статус скоро обновится');
    } catch (e) { setError(e.message); }
    finally { setSending(false); }
  }

  return <main>
    <h1>Отпуск</h1>
    <p>{keycloak.tokenParsed.preferred_username} <button onClick={() => keycloak.logout({ redirectUri: 'http://localhost:3000' })}>Выйти</button></p>
    {error && <p role="alert">Ошибка: {error} <button onClick={() => setError('')}>Закрыть</button></p>}
    {message && <p role="status">{message}</p>}
    {employee && <>
      <h2>Новая заявка</h2>
      <p>{me?.name}. Доступно дней с учётом резервов: {me?.balance_days ?? '…'}.</p>
      <form onSubmit={submit}>
        <fieldset disabled={sending}>
          <label>С <input type="date" name="start_date" min={today} value={form.start_date} onChange={change} required/></label>
          <label>По <input type="date" name="end_date" min={form.start_date || today} value={form.end_date} onChange={change} required/></label>
          <label>Комментарий <textarea name="comment" maxLength={1000} value={form.comment} onChange={change}/></label>
          <button type="submit">{sending ? 'Отправка' : 'Подать заявку'}</button>
        </fieldset>
      </form>
      <p>Календарные дни включительно. До 2 дней - автоодобрение, свыше 14 дней после одобрения руководителем требуется HR.</p>
    </>}
    {manager && <p>Решения принимаются в <a href="http://localhost:8082/tasklist/" target="_blank" rel="noreferrer">Camunda Tasklist</a> (manager / manager).</p>}
    <h2>{manager ? 'Заявки сотрудников' : 'Мои заявки'}</h2>
    <table>
      <thead><tr><th>Дата подачи</th>{manager && <th>Сотрудник</th>}<th>Период</th><th>Дней</th><th>Статус</th></tr></thead>
      <tbody>{requests.map(r => <tr key={r.id} title={r.id}>
        <td>{new Date(r.created_at).toLocaleString('ru-RU')}</td>{manager && <td>{r.employee_id}</td>}
        <td>{r.start_date} - {r.end_date}</td><td>{r.days}</td><td>{statuses[r.status] || r.status}</td>
      </tr>)}</tbody>
    </table>
    {requests.length === 0 && <p>Заявок пока нет.</p>}
    <p><button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>Назад</button> Страница {offset / limit + 1} <button disabled={requests.length < limit} onClick={() => setOffset(offset + limit)}>Далее</button></p>
  </main>;
}
