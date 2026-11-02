import { requestResponse } from '../../api/client';
import type {
  ModelGroup,
  ModelGroupAddResult,
  ModelGroupList,
  ModelGroupRestoreResult,
} from '../../types/modelGroups';

/**
 * The model-group management surface, beside the pages that own it.
 *
 * These are ordinary management routes, but they are declared here rather than in
 * `api/client.ts` for the same reason the Agent's runtime protocol is: the group pages are lazy
 * and are the only readers, and keeping their endpoints with them leaves the shared transport
 * module to speak for the console shell rather than for every feature added later. Every call
 * still goes through `requestResponse`, so authentication, the one error shape and the demo's
 * write notice are decided in one place.
 */
async function read<T>(path: string, options: RequestInit = {}): Promise<T> {
  return (await requestResponse(path, options)).json() as Promise<T>;
}

function groupPath(id: string): string {
  return `/management/model-groups/${encodeURIComponent(id)}`;
}

export const modelGroupApi = {
  list(): Promise<ModelGroupList> {
    return read<ModelGroupList>('/management/model-groups', { method: 'GET' });
  },

  create(name: string): Promise<ModelGroup> {
    return read<ModelGroup>('/management/model-groups', { method: 'POST', body: JSON.stringify({ name }) });
  },

  rename(id: string, name: string): Promise<ModelGroup> {
    return read<ModelGroup>(groupPath(id), { method: 'PATCH', body: JSON.stringify({ name }) });
  },

  async remove(id: string): Promise<void> {
    await requestResponse(groupPath(id), { method: 'DELETE' });
  },

  /**
   * Appends models after the current last member. A model already present is reported as skipped
   * rather than refused, so a multi-select that repeats one name still adds the rest.
   */
  addMembers(id: string, models: readonly string[]): Promise<ModelGroupAddResult> {
    return read<ModelGroupAddResult>(`${groupPath(id)}/members`, {
      method: 'POST',
      body: JSON.stringify({ models }),
    });
  },

  removeMember(id: string, model: string): Promise<ModelGroup> {
    return read<ModelGroup>(`${groupPath(id)}/members/${encodeURIComponent(model)}`, { method: 'DELETE' });
  },

  /** The complete membership exactly once; a short list is refused rather than silently dropping rows. */
  reorder(id: string, models: readonly string[]): Promise<ModelGroup> {
    return read<ModelGroup>(`${groupPath(id)}/order`, { method: 'PUT', body: JSON.stringify({ models }) });
  },

  /** Recreates whichever preset groups are missing, empty; groups that still exist are untouched. */
  restoreDefaults(): Promise<ModelGroupRestoreResult> {
    return read<ModelGroupRestoreResult>('/management/model-groups/restore-defaults', { method: 'POST' });
  },
};
