import Icon from './Icon';

export default function Brand() {
  return (
    <a className="brand" href="/" aria-label="SafeVault home">
      <span className="brand-mark">
        <Icon name="vault" />
      </span>
      <span>
        Safe<span className="brand-accent">Vault</span>
      </span>
    </a>
  );
}
