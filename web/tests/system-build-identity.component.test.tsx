import { render, screen, within } from '@testing-library/react';
import { beforeEach, expect, it } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { BuildIdentity } from '../src/components/system/BuildIdentity';

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

it('renders supplied source identity without replacing it with an upstream release', () => {
  const identity = { version: 'v2.3.4-local', revision: 'abcdef0123456789abcdef0123456789abcdef01', ref: 'refs/heads/review-branch' };
  render(<I18nProvider><BuildIdentity identity={identity} /></I18nProvider>);
  const section = screen.getByRole('region', { name: 'OMC fork build' });
  expect(within(section).getByText(identity.version).textContent).toBe(identity.version);
  expect(within(section).getByText(identity.revision.slice(0, 12)).getAttribute('title')).toBe(identity.revision);
  expect(within(section).getByText(identity.ref).textContent).toBe(identity.ref);
});

it('renders unknown for every absent observation including an older server response', () => {
  const view = render(<I18nProvider><BuildIdentity /></I18nProvider>);
  expect(screen.getAllByText('Unknown')).toHaveLength(3);
  view.rerender(<I18nProvider><BuildIdentity identity={{ version: '', revision: '', ref: '' }} /></I18nProvider>);
  expect(screen.getAllByText('Unknown')).toHaveLength(3);
});
