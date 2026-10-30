import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { OllamaCreditsView } from '../src/components/ollama/OllamaCreditsView';
import type { NormalizedQuota } from '../src/types/quota';

function reading(overrides: Record<string, unknown> = {}): NormalizedQuota {
  return { provider: 'ollama', status: 'healthy', windows: [], plan: { plan_label: 'Max' },
    ollama_credits: { source: 'settings_usage_credits', remaining_cents: 29998, used_cents: 2,
      refill_text: 'Monthly credits refill in 2 weeks', models: [{ model: 'fake-model:1017', requests: 15 }], ...overrides },
  } as unknown as NormalizedQuota;
}

function balanceReading(overrides: Record<string, unknown> = {}): NormalizedQuota {
  return { provider: 'ollama', status: 'healthy', windows: [],
    ollama_credits: { source: 'balance_api', remaining_cents: 1250, used_cents: 750,
      total_cents: 2000, used_percent: 37.5,
      models: [{ model: 'llama3.3:70b', requests: 42 }], ...overrides },
  } as unknown as NormalizedQuota;
}

function show(quota?: NormalizedQuota, status = 'ok') {
  render(<I18nProvider><OllamaCreditsView quota={quota} status={status} /></I18nProvider>);
  return screen.getByTestId('ollama-credits').textContent ?? '';
}

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

describe('Ollama USD credits', () => {
  it('shows the exact two USD amounts, computed total, labelled share, refill and monthly model counts', () => {
    const text = show(reading());
    expect(text).toContain('$299.98');
    expect(text).toContain('$0.02');
    expect(text).toContain('$300.00');
    expect(text).toContain('<0.01%');
    expect(text).toContain('Calculated from used / total');
    expect(text).toContain('Monthly credits refill in 2 weeks');
    expect(text).toContain('fake-model:1017');
    expect(text).toContain('15 requests');
    expect(text).not.toMatch(/deplet|exhaust|reset at/i);
  });

  it('distinguishes published zero from missing data without rendering zero money or percentages', () => {
    const text = show(reading({ remaining_cents: 0, used_cents: 0 }));
    expect(text).toContain('No credits remaining');
    expect(text).toContain('No usage this month');
    expect(text).not.toMatch(/\$|%|Data missing/);
  });

  it('reports missing data after a failed fetch even if a previous snapshot exists', () => {
    const text = show(reading(), 'transport_error');
    expect(text).toContain('Data missing');
    expect(text).not.toMatch(/\$|%|15 requests|in 2 weeks/);
  });

  it('refuses path-contaminated amounts without fabricating a share', () => {
    const text = show(reading({ used_cents: '/usr/bin/bash.02' }));
    expect(text).toContain('Data missing');
    expect(text).not.toMatch(/bash|\$0|%|\$300/);
  });

  it('shows a missing reading explicitly', () => {
    expect(show()).toContain('Data missing');
  });

  it('does not round a small positive share to zero percent', () => {
    const text = show(reading({ remaining_cents: 9998, used_cents: 2 }));
    expect(text).toContain('0.02%');
  });

  it('renders balance_api credits and model requests instead of discarding the source', () => {
    const text = show(balanceReading());
    expect(text).toContain('$12.50');
    expect(text).toContain('$7.50');
    expect(text).toContain('$20.00');
    expect(text).toContain('37.5%');
    expect(text).toContain('llama3.3:70b');
    expect(text).toContain('42 requests');
    expect(text).not.toContain('Data missing');
  });

  it('keeps balance_api USD amounts visible when model requests are not reported', () => {
    const text = show(balanceReading({ models: undefined }));
    expect(text).toContain('$12.50');
    expect(text).toContain('$7.50');
    expect(text).toContain('$20.00');
    expect(text).toContain('Not reported');
    expect(text).not.toContain('Data missing');
  });
});
