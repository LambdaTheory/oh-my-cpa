/**
 * The rail's group sizes, in rail order, for the shell placeholder.
 *
 * `ShellLoading` paints before the navigation model (and every icon in it) has downloaded, and it
 * stays eager, so it cannot import `navigation.tsx` without pulling the whole rail into the first
 * paint. The counts live here as a leaf module instead; a component test pins them to
 * `buildNavGroups([])` so adding or moving an entry without updating the placeholder fails a test
 * rather than making the rail grow under the reader when it lands.
 */
export const SHELL_NAV_GROUP_SIZES = [4, 3, 5, 3, 2, 3] as const;

/** The demonstration closes the Playground, so the Operate group draws one row less. */
export const SHELL_NAV_GROUP_SIZES_DEMO = [3, 3, 5, 3, 2, 3] as const;

export function shellNavGroupSizes(isDemo: boolean): readonly number[] {
  return isDemo ? SHELL_NAV_GROUP_SIZES_DEMO : SHELL_NAV_GROUP_SIZES;
}
