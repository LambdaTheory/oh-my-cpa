import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(root, 'web', 'package.json'));

/**
 * Vite has to parse every module reachable from an import specifier. Importing
 * this collection as React components turns a single icon lookup into thousands
 * of modules and a multi-megabyte eager chunk. The package ships the same marks
 * as plain SVG assets, so the app copies them into its public directory and
 * resolves them by URL instead.
 *
 * `target` is a test seam: regression coverage runs the real copy into an
 * isolated public directory under an actual Vite server. Production callers use
 * the repository path.
 */
export function syncLobeIcons({ quiet = false, target = path.join(root, 'web', 'public', 'lobe-icons') } = {}) {
  const packageRoot = path.dirname(require.resolve('@lobehub/icons-static-svg/package.json'));
  const source = path.join(packageRoot, 'icons');
  const catalog = JSON.parse(fs.readFileSync(path.join(root, 'web', 'src', 'generated', 'lobeIconCatalog.json'), 'utf8'));
  const slug = (iconId) => iconId.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

  if (!fs.existsSync(source)) {
    throw new Error(`static Lobe icon source is missing: ${source}`);
  }

  const staging = `${target}.tmp-${process.pid}`;
  fs.rmSync(staging, { recursive: true, force: true });
  fs.mkdirSync(staging, { recursive: true });
  let count = 0;
  for (const item of catalog) {
    const names = [`${slug(item.id)}.svg`];
    if (item.hasColor) names.push(`${slug(item.id)}-color.svg`);
    for (const name of names) {
      const sourceFile = path.join(source, name);
      if (!fs.existsSync(sourceFile)) throw new Error(`missing static Lobe icon: ${name}`);
      fs.copyFileSync(sourceFile, path.join(staging, name));
      count += 1;
    }
  }
  fs.rmSync(target, { recursive: true, force: true });
  fs.renameSync(staging, target);

  if (!quiet) console.log(`synced ${count} Lobe icons to ${path.relative(root, target)}`);
}

/**
 * Vite snapshots its public-file inventory while creating a dev server, before
 * `buildStart` runs. A dev server that generates the icons in `buildStart` has
 * already decided they are absent, so requests for them fall through to the SPA
 * shell and return HTML with a 200. Generate during config resolution instead.
 *
 * Builds have no such snapshot. Their watcher calls `buildStart` again on every
 * rebuild, so builds keep regenerating there. Repeating that in dev would race
 * the public-directory watcher with the generation rename.
 */
export function lobeIconsPlugin({ quiet = false, sync = syncLobeIcons } = {}) {
  let command;
  return {
    name: 'sync-lobe-icons',
    enforce: 'pre',
    configResolved(config) {
      command = config.command;
      if (command === 'serve') sync({ quiet });
    },
    buildStart() {
      if (command === 'build') sync({ quiet });
    },
  };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  syncLobeIcons();
}
