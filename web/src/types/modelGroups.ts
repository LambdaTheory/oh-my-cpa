/**
 * Model groups: an operator-named, ordered list of model names kept for one purpose.
 *
 * The wire shape is the handwritten DTO allowlist in `internal/api/model_groups.go`; this module
 * adds only the decisions the console makes on top of it - what a valid name is, how one move
 * changes an order, and how a write's answer replaces what the list already shows. Keeping those
 * here rather than inside the pages lets the logic suite decide them without a browser.
 */

export interface ModelGroupMember {
  model: string;
  /** 1-based slot inside this group; the same model has an independent slot in every group. */
  position: number;
  added_at_ms: number;
}

export interface ModelGroup {
  id: string;
  name: string;
  /** One of the five groups a deployment starts with. It renames and deletes like any other. */
  is_preset: boolean;
  created_at_ms: number;
  updated_at_ms: number;
  /** In the operator's order, positions 1..n. */
  members: ModelGroupMember[];
}

export interface ModelGroupList {
  groups: ModelGroup[];
}

export interface ModelGroupAddResult {
  group: ModelGroup;
  /** Models this request appended. */
  added: string[];
  /** Models the group already held; the request is still a success. */
  skipped: string[];
}

export interface ModelGroupRestoreResult {
  created: ModelGroup[];
  /** Preset names that were still present and were therefore left untouched. */
  skipped: string[];
}

/** A name is held to this many runes, matching `repository.ModelGroupNameLimit`. */
export const MODEL_GROUP_NAME_LIMIT = 40;

/** One cache entry for the whole list: every page resolves a group from it, including by id. */
export const MODEL_GROUPS_QUERY_KEY = ['model-groups'] as const;

export type ModelGroupNameError = 'empty' | 'too_long' | 'conflict';

/**
 * Why a name cannot be saved, or null when it can.
 *
 * The server enforces the same rules; checking here first turns a round trip into an inline
 * message and keeps a conflict from closing the dialog.
 */
export function modelGroupNameError(
  name: string,
  groups: readonly ModelGroup[],
  exceptId?: string,
): ModelGroupNameError | null {
  const trimmed = name.trim();
  if (trimmed === '') return 'empty';
  if ([...trimmed].length > MODEL_GROUP_NAME_LIMIT) return 'too_long';
  const folded = trimmed.toLocaleLowerCase();
  if (groups.some((group) => group.id !== exceptId && group.name.toLocaleLowerCase() === folded)) return 'conflict';
  return null;
}

/**
 * The order after moving one member by `delta` slots.
 *
 * A move off either end returns the input array itself, so a caller can treat "same reference" as
 * "nothing to save" and the boundary buttons cannot send a request that changes nothing.
 */
export function moveModelGroupMember(
  models: readonly string[],
  index: number,
  delta: -1 | 1,
): string[] {
  const target = index + delta;
  if (index < 0 || index >= models.length || target < 0 || target >= models.length) return models as string[];
  const next = [...models];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

export function modelGroupById(list: ModelGroupList | undefined, id: string | undefined): ModelGroup | undefined {
  if (!list || !id) return undefined;
  return list.groups.find((group) => group.id === id);
}

/** A stored group's members in the given order, renumbered 1..n. */
export function withModelOrder(group: ModelGroup, models: readonly string[]): ModelGroup {
  const addedAt = new Map(group.members.map((member) => [member.model, member.added_at_ms]));
  return {
    ...group,
    members: models.map((model, index) => ({ model, position: index + 1, added_at_ms: addedAt.get(model) ?? Date.now() })),
  };
}

/**
 * The list with `group` replaced (or appended).
 *
 * A write's response is newer than the list the page read, so applying it directly keeps the page
 * current without waiting for a refetch; the pages still invalidate afterwards so a grouped read
 * resolves against the server.
 */
export function withModelGroup(list: ModelGroupList | undefined, group: ModelGroup): ModelGroupList {
  const groups = list?.groups ?? [];
  const index = groups.findIndex((candidate) => candidate.id === group.id);
  if (index < 0) return { groups: [...groups, group] };
  return { groups: groups.map((candidate) => (candidate.id === group.id ? group : candidate)) };
}

export function withoutModelGroup(list: ModelGroupList | undefined, id: string): ModelGroupList {
  return { groups: (list?.groups ?? []).filter((group) => group.id !== id) };
}

/** The list with one group's members reordered, for an optimistic move before the server answers. */
export function reorderModelGroupInList(
  list: ModelGroupList | undefined,
  id: string,
  models: readonly string[],
): ModelGroupList {
  if (!list) return { groups: [] };
  return {
    groups: list.groups.map((group) => (group.id === id ? withModelOrder(group, models) : group)),
  };
}

/** The server's stable refusal code, when the failure carried one. */
export function modelGroupRefusalCode(error: unknown): string | undefined {
  const data = (error as { data?: unknown } | null)?.data;
  if (!data || typeof data !== 'object') return undefined;
  const code = (data as { code?: unknown }).code;
  return typeof code === 'string' && code !== '' ? code : undefined;
}
