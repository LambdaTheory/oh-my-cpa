/**
 * Model groups: an operator-named, ordered list of model names kept for one purpose.
 *
 * The wire shape is the handwritten DTO allowlist in `internal/api/model_groups.go`; this module
 * adds only the decisions the console makes on top of it - what a valid name is, how one move
 * changes an order, and how a write's answer replaces what the list already shows. Keeping those
 * here rather than inside the pages lets the logic suite decide them without a browser.
 */

import { formatUsd } from './pricingDisplay';

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

/**
 * One model's reading from `GET /management/model-groups/readings`.
 *
 * The band and the balance are the same figures `model_groups_models` reports, projected by the
 * same server code for the same model; the console and a caller therefore cannot disagree. A
 * reading exists for every group member and every advertised model - candidates included - so a
 * page joins by name instead of asking per row.
 */
export type ModelGroupAvailability = 'available' | 'tight' | 'unknown' | 'unavailable';

export interface ModelGroupBalance {
  /** Decimal string, kept as text so no precision is lost between server and page. */
  amount: string;
  currency: string;
}

export interface ModelGroupReading {
  model: string;
  availability: ModelGroupAvailability;
  balance: ModelGroupBalance | null;
}

export interface ModelGroupReadings {
  models: ModelGroupReading[];
}

/** The bands a group card counts, in the availability order the PRD fixed. */
export const MODEL_GROUP_AVAILABILITY_BANDS: readonly ModelGroupAvailability[] = [
  'available',
  'tight',
  'unknown',
  'unavailable',
];

export function emptyModelGroupAvailabilityCounts(): Record<ModelGroupAvailability, number> {
  return { available: 0, tight: 0, unknown: 0, unavailable: 0 };
}

/** One cache entry for the whole list: every page resolves a group from it, including by id. */
export const MODEL_GROUPS_QUERY_KEY = ['model-groups'] as const;

/** The readings the group pages join against; kept beside the list they annotate. */
export const MODEL_GROUP_READINGS_QUERY_KEY = ['model-group-readings'] as const;

/** A name's reading, or the unknown reading a name without one is drawn as. */
export function modelGroupReading(
  readings: ModelGroupReadings | undefined,
  model: string,
): ModelGroupReading {
  const found = readings?.models.find((reading) => reading.model === model);
  return found ?? { model, availability: 'unknown', balance: null };
}

/**
 * How many members of one group fall into each band.
 *
 * A member without a reading counts as unknown, the same answer the per-group query gives for a
 * name it cannot measure; leaving it out would make the counts add up to less than the group.
 */
export function countModelGroupAvailability(
  models: readonly string[],
  readings: ModelGroupReadings | undefined,
): Record<ModelGroupAvailability, number> {
  const counts = emptyModelGroupAvailabilityCounts();
  for (const model of models) counts[modelGroupReading(readings, model).availability] += 1;
  return counts;
}

/** The balance a row shows: the exact amount with its currency, or the empty-cell dash. */
export function formatModelGroupBalance(balance: ModelGroupBalance | null | undefined): string {
  if (!balance || balance.amount === '') return '';
  const amount = Number(balance.amount);
  if (!Number.isFinite(amount)) return `${balance.amount} ${balance.currency}`;
  if (!balance.currency || balance.currency === 'USD') {
    return formatUsd(amount);
  }
  try {
    return new Intl.NumberFormat('en-US', { style: 'currency', currency: balance.currency }).format(amount);
  } catch {
    return `${balance.amount} ${balance.currency}`;
  }
}

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

/**
 * The list after a refused reorder, with only that group's order put back.
 *
 * A rollback that restored a whole group snapshot would also undo a rename, a member removal
 * or an add that landed after the snapshot was taken - and a page that calls a removed model
 * back is worse than one that leaves a moved row where it landed. The order is therefore
 * intersected with the membership the group holds now, and any member this order has never
 * seen stays appended rather than disappearing.
 */
export function restoreModelOrder(
  list: ModelGroupList | undefined,
  id: string,
  order: readonly string[],
): ModelGroupList {
  if (!list) return { groups: [] };
  return {
    groups: list.groups.map((group) => {
      if (group.id !== id) return group;
      const present = group.members.map((member) => member.model);
      const presentSet = new Set(present);
      const restored = order.filter((model) => presentSet.has(model));
      for (const model of present) {
        if (!restored.includes(model)) restored.push(model);
      }
      return withModelOrder(group, restored);
    }),
  };
}

/** The server's stable refusal code, when the failure carried one. */
export function modelGroupRefusalCode(error: unknown): string | undefined {
  const data = (error as { data?: unknown } | null)?.data;
  if (!data || typeof data !== 'object') return undefined;
  const code = (data as { code?: unknown }).code;
  return typeof code === 'string' && code !== '' ? code : undefined;
}
