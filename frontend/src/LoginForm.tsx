import { useState, type FormEvent } from 'react';
import { login, type Session } from './api';
import Brand from './Brand';
import Icon from './Icon';

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
      <div className="auth-intro">
        <Brand />
        <div>
          <p className="eyebrow">YOUR DOCUMENT WORKSPACE</p>
          <h1>
            Your documents,
            <br />
            all in one place.
          </h1>
          <p>Keep your files organised and find what you need.</p>
          <ul className="auth-features">
            <li>
              <Icon name="upload" /> Upload your documents
            </li>
            <li>
              <Icon name="search" /> Search filenames and contents
            </li>
            <li>
              <Icon name="documents" /> Preview and download files
            </li>
          </ul>
        </div>
      </div>
      <section className="auth-card" aria-labelledby="signin-title">
        <h2 id="signin-title">Sign in</h2>
        <p>Use your SafeVault account to continue.</p>
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
        <p className="muted">Need an account? Contact your administrator.</p>
      </section>
    </main>
  );
}
