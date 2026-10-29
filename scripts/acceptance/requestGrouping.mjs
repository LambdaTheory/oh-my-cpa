/**
 * Readiness of the request list after a grouping change.
 *
 * The list keeps the previous grouping's DOM until the remount commits, and the
 * first row id is the same in both groupings. Only the first group title differs,
 * so a wait that omits it can resolve against the stale grouping and then drive a
 * holder that is about to be replaced.
 */
export function isRequestGroupingReady({ groupTitleCount, firstGroupTitle, firstRowId }, expected) {
  return groupTitleCount > 0
    && firstGroupTitle.trim() === expected.groupTitle
    && firstRowId.trim() === expected.firstRowId;
}
