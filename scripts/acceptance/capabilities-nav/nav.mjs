// Readers and drivers for the console shell: the rail (R1-R3, R6, R8, R11), breadcrumb (R4), tab title (R5),
// the phone drawer (R7), the language menu (R10) and the sidebar settings panel (R9). They read the same hooks
// the console's own probes use (see cases.md 0.6); a hook that is missing is reported by the caller, never papered over.
import { LANGUAGE_NAMES, until } from './lib.mjs';

/** Waits for the signed-in shell: the rail has entries. */
export async function waitForShell(page, { timeout = 30_000 } = {}) {
  await page.locator('.app-menu li[data-route-path]').first().waitFor({ state: 'attached', timeout });
}

/** The whole rail as data: groups with their entries in document order, plus the flat entry list. */
export async function readRail(page) {
  return page.evaluate(() => {
    const menu = document.querySelector('.app-menu');
    if (!menu) return null;
    const sider = document.querySelector('.ant-layout-sider');
    const iconSignature = (node) => {
      const svg = node.querySelector('svg');
      return svg ? svg.innerHTML.replace(/\s+/g, '') : '';
    };
    const describe = (node) => ({
      path: node.getAttribute('data-route-path'),
      name: (node.textContent ?? '').replace(/\s+/g, ' ').trim(),
      title: node.getAttribute('title'),
      selected: node.classList.contains('ant-menu-item-selected'),
      icon: iconSignature(node),
      tabbable: node.tabIndex,
    });
    const groups = [...menu.querySelectorAll('.ant-menu-item-group')].map((group) => ({
      title: (group.querySelector('.ant-menu-item-group-title')?.textContent ?? '').replace(/\s+/g, ' ').trim(),
      titleChildren: group.querySelector('.ant-menu-item-group-title')?.children.length ?? -1,
      items: [...group.querySelectorAll('[data-route-path]')].map(describe),
    }));
    const flat = [...menu.querySelectorAll('[data-route-path]')].map(describe);
    return {
      isCollapsed: Boolean(sider?.classList.contains('ant-layout-sider-collapsed')),
      groupTitles: [...menu.querySelectorAll('.ant-menu-item-group-title')].map((node) => (node.textContent ?? '').replace(/\s+/g, ' ').trim()),
      groups, flat,
      selected: flat.filter((entry) => entry.selected).map((entry) => entry.path),
    };
  });
}

/** Breadcrumb as the console draws it: group text, current-page text. */
export async function readBreadcrumb(page) {
  return page.evaluate(() => {
    const crumb = document.querySelector('.app-breadcrumb');
    if (!crumb) return null;
    const links = [...crumb.querySelectorAll('.ant-breadcrumb-link')].map((node) => (node.textContent ?? '').replace(/\s+/g, ' ').trim());
    return {
      group: links[0] ?? '',
      current: (crumb.querySelector('.app-breadcrumb-current')?.textContent ?? '').replace(/\s+/g, ' ').trim(),
      text: (crumb.textContent ?? '').replace(/\s+/g, ' ').trim(),
    };
  });
}

export async function readTitle(page) {
  return page.title();
}

/** Everything the cases read from one page at once: address, title, rail selection, breadcrumb, and whether it stands outside the shell (R12). */
export async function readLocation(page) {
  const url = new URL(page.url());
  const [rail, crumb, title, outside, heading] = await Promise.all([
    readRail(page), readBreadcrumb(page), readTitle(page),
    page.evaluate(() => !document.querySelector('.ant-layout-sider') && !document.querySelector('.app-breadcrumb')),
    page.evaluate(() => (document.querySelector('main h1, .page-header h1, h1')?.textContent ?? '').replace(/\s+/g, ' ').trim()),
  ]);
  return { path: url.pathname, search: url.search, title, selected: rail?.selected ?? [], crumb, isOutsideShell: outside, heading };
}

/** Opens a console path and waits for the shell (or, for pages outside it, for the document to settle). */
export async function open(page, url, { shell = true, timeout = 30_000 } = {}) {
  await page.goto(url, { waitUntil: 'domcontentloaded', timeout });
  if (shell) await waitForShell(page, { timeout });
}

/** Clicks the header's language menu and picks `id`; returns once the rail's group titles stop changing language. */
export async function switchLanguage(page, id) {
  const trigger = page.locator('.language-trigger-code').first();
  await trigger.click();
  const item = page.locator('.ant-dropdown-menu-item', { hasText: LANGUAGE_NAMES[id] }).first();
  await item.waitFor({ state: 'visible' });
  // The trigger's own tooltip sits over the menu while the pointer is still on the trigger; moving away dismisses it, as a hand would.
  await page.mouse.move(2, 2);
  await until(async () => (await page.locator('.ant-tooltip:not(.ant-tooltip-hidden)').count()) === 0, { label: 'tooltip dismissed' });
  await item.click();
  await until(async () => (await page.evaluate(() => localStorage.getItem('omc-lang'))) === id, { label: `stored language ${id}` });
}

/** Collapses the rail through the header button and waits until the sider reports it. */
export async function collapseRail(page) {
  const button = page.locator('.app-header-left button').first();
  await button.click();
  await until(async () => (await readRail(page))?.isCollapsed === true, { label: 'rail collapsed' });
}

export async function expandRail(page) {
  const button = page.locator('.app-header-left button').first();
  await button.click();
  await until(async () => (await readRail(page))?.isCollapsed === false, { label: 'rail expanded' });
}

/** Phone layout: opens the drawer through the header's first button and waits for its menu. */
export async function openDrawer(page) {
  await page.locator('.app-header-left button').first().click();
  await page.locator('.mobile-nav-drawer .app-menu li[data-route-path]').first().waitFor({ state: 'visible' });
}

/** The drawer's menu as data (same shape as readRail but scoped to the drawer). */
export async function readDrawer(page) {
  return page.evaluate(() => {
    const menu = document.querySelector('.mobile-nav-drawer .app-menu');
    if (!menu) return null;
    const groups = [...menu.querySelectorAll('.ant-menu-item-group')].map((group) => ({
      title: (group.querySelector('.ant-menu-item-group-title')?.textContent ?? '').replace(/\s+/g, ' ').trim(),
      items: [...group.querySelectorAll('[data-route-path]')].map((node) => ({ path: node.getAttribute('data-route-path'), name: (node.textContent ?? '').replace(/\s+/g, ' ').trim() })),
    }));
    return { groups, flat: [...menu.querySelectorAll('[data-route-path]')].map((node) => node.getAttribute('data-route-path')), scrollWidth: document.documentElement.scrollWidth, innerWidth: window.innerWidth };
  });
}

/** Loading placeholder rail (R11). */
export async function readPlaceholder(page) {
  return page.evaluate(() => {
    const sider = document.querySelector('.shell-loading-sider');
    if (!sider) return null;
    const nav = sider.querySelector('.shell-loading-nav');
    const groups = [...sider.querySelectorAll('.shell-loading-nav-group')].map((group) => group.querySelectorAll('.shell-loading-nav-item').length);
    return { groups, navScrollHeight: nav?.scrollHeight ?? 0, navHeight: nav?.getBoundingClientRect().height ?? 0, siderHeight: sider.getBoundingClientRect().height };
  });
}

/** The real rail's measured content height and one entry's height (cases.md C-43 step 4). */
export async function readRailMetrics(page) {
  return page.evaluate(() => {
    const menu = document.querySelector('.app-menu');
    const viewport = document.querySelector('.app-sider-viewport');
    const entry = menu?.querySelector('li[data-route-path]');
    return {
      menuHeight: menu?.getBoundingClientRect().height ?? 0,
      menuScrollHeight: menu?.scrollHeight ?? 0,
      viewportScrollHeight: viewport?.scrollHeight ?? 0,
      viewportClientHeight: viewport?.clientHeight ?? 0,
      entryHeight: entry?.getBoundingClientRect().height ?? 0,
    };
  });
}

/** Computed style fields for a group title (C-04). */
export async function groupTitleStyles(page, titleText) {
  return page.evaluate((wanted) => {
    const nodes = [...document.querySelectorAll('.app-menu .ant-menu-item-group-title')];
    const node = nodes.find((candidate) => (candidate.textContent ?? '').trim().toLowerCase() === wanted.toLowerCase());
    if (!node) return null;
    const style = getComputedStyle(node);
    const before = getComputedStyle(node, '::before');
    const after = getComputedStyle(node, '::after');
    const box = node.getBoundingClientRect();
    return {
      fontSize: style.fontSize, fontWeight: style.fontWeight, color: style.color, textAlign: style.textAlign,
      textTransform: style.textTransform, letterSpacing: style.letterSpacing, lineHeight: style.lineHeight,
      paddingLeft: style.paddingLeft, marginLeft: style.marginLeft, children: node.children.length,
      beforeContent: before.content, afterContent: after.content, boxLeft: box.left, boxHeight: box.height,
      rect: { x: box.x, y: box.y, width: box.width, height: box.height },
    };
  }, titleText);
}

// ---------------------------------------------------------------------------------------------
// Sidebar settings panel (R9)
// ---------------------------------------------------------------------------------------------

/** Opens /omc-settings and then the sidebar entries panel ("Manage entries"). */
export async function openSidebarPanel(page, app) {
  await open(page, `${app}/omc-settings`);
  await page.locator('[data-testid="sidebar-nav-settings"]').waitFor();
  await page.locator('[data-testid="sidebar-nav-settings"] button').first().click();
  await page.locator('[data-testid="sidebar-nav-dialog"]').waitFor();
}

/** The settings row's summary text ("N hidden" / "Every entry is shown"), read from the page behind the panel. */
export async function readSidebarSummary(page) {
  return page.evaluate(() => {
    const root = document.querySelector('[data-testid="sidebar-nav-settings"]');
    const button = root?.querySelector('button');
    const full = (root?.textContent ?? '').replace(/\s+/g, ' ').trim();
    const withoutButton = button ? full.replace((button.textContent ?? '').trim(), '').trim() : full;
    return { full, withoutButton };
  });
}

/**
 * The panel as data, in document order: group subheads, and per entry its address, label, switch state,
 * whether the switch is disabled, and the row's own description (the lock reason).
 */
export async function readSidebarPanel(page) {
  return page.evaluate(() => {
    const dialog = document.querySelector('[data-testid="sidebar-nav-dialog"]');
    if (!dialog) return null;
    const groups = [];
    let current = null;
    const nodes = dialog.querySelectorAll('h4, [data-nav-toggle]');
    let headings = 0;
    for (const node of nodes) {
      if (node.tagName === 'H4') {
        // The first h4 heads the whole list ("Sidebar entries"), beside the Show all button; the rest are group subheads.
        headings += 1;
        if (headings === 1) continue;
        current = { title: (node.textContent ?? '').replace(/\s+/g, ' ').trim(), entries: [] };
        groups.push(current);
      } else if (current) {
        const toggle = node.querySelector('[role="switch"]');
        const row = node.closest('[class*="row" i], [class*="Row"]') ?? node.parentElement;
        current.entries.push({
          path: node.getAttribute('data-nav-toggle'),
          label: toggle?.getAttribute('aria-label') ?? '',
          isOn: toggle?.getAttribute('aria-checked') === 'true',
          isDisabled: Boolean(toggle?.disabled),
          rowText: (row?.textContent ?? '').replace(/\s+/g, ' ').trim(),
        });
      }
    }
    const showAll = [...dialog.querySelectorAll('button')].find((button) => !button.getAttribute('role'));
    return { groups, showAllDisabled: showAll ? showAll.disabled : null, showAllText: (showAll?.textContent ?? '').trim() };
  });
}

/** Toggles the switch of one entry in the open panel. */
export async function toggleSidebarEntry(page, routePath) {
  await page.locator(`[data-nav-toggle="${routePath}"] [role="switch"]`).click();
}
