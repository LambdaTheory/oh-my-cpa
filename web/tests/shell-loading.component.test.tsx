import { render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { ThemeProvider } from '../src/theme/ThemeContext';
import { ShellLoading } from '../src/components/common/ShellLoading';
import { buildNavGroups } from '../src/components/common/navigation';

/**
 * The shell placeholder's rail against the real rail model.
 *
 * `ShellLoading` paints before the navigation module downloads, so it carries its group sizes in a
 * leaf module rather than importing `navigation.tsx`. These assertions render the placeholder and
 * count what it draws, so the counts are pinned to the rail the reader lands on rather than to a
 * second copy of the numbers; an entry added or moved without updating the placeholder fails here.
 */

function mount() {
  return render(
    <ThemeProvider>
      <I18nProvider>
        <ShellLoading />
      </I18nProvider>
    </ThemeProvider>,
  );
}

function drawnGroupSizes(container: HTMLElement): number[] {
  return [...container.querySelectorAll('.shell-loading-nav-group')].map(
    (group) => group.querySelectorAll('.shell-loading-nav-item').length,
  );
}

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  }));
});

describe('shell placeholder rail', () => {
  it('draws one group per rail group with the same number of items', () => {
    window.__OMCPA_CONFIG__ = { demo: false };
    const { container } = mount();
    expect(drawnGroupSizes(container)).toEqual(buildNavGroups([]).map((group) => group.items.length));
    expect(drawnGroupSizes(container)).toEqual([4, 3, 5, 3, 2, 3]);
  });

  it('draws the demonstration rail without the Playground row', () => {
    window.__OMCPA_CONFIG__ = { demo: true };
    const { container } = mount();
    expect(drawnGroupSizes(container)).toEqual(buildNavGroups([]).map((group) => group.items.length));
    expect(drawnGroupSizes(container)).toEqual([3, 3, 5, 3, 2, 3]);
  });
});
