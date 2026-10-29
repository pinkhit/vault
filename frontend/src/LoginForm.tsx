import { useState, type FormEvent } from 'react';
import { login, type Session } from './api';

export default function LoginForm({
  onLogin,
}: {
  onLogin: (session: Session) => void;
}) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      const session = await login(username, password);
      setPassword('');
      onLogin(session);
    } catch (error: unknown) {
      setPassword('');
      setError(
        error instanceof Error ? error.message : 'Sign-in failed. Try again.',
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="auth-page">
      <h1>FamilyVault</h1>
      <p>Private documents. Individual accounts.</p>
      <section className="auth-card" aria-labelledby="signin-title">
        <h2 id="signin-title">Sign in</h2>
        <form onSubmit={(event) => void submit(event)}>
          <label htmlFor="username">Username</label>
          <input
            id="username"
            autoComplete="username"
            required
            maxLength={64}
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            disabled={busy}
          />
          <label htmlFor="password">Password</label>
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            required
            maxLength={1024}
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            disabled={busy}
          />
          <button className="primary" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <p className="muted">
          Ask your administrator for an account. Initial setup uses the offline
          create-admin command in the README.
        </p>
      </section>
      <p className="warning">
        <strong>Synthetic documents only.</strong> Localhost development;
        production backups and deployment are not ready.
      </p>
    </main>
  );
}
