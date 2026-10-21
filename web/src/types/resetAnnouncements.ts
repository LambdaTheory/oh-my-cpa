export interface ResetAnnouncementInput {
  provider: string;
  source: 'manual';
  expected_reset_at_ms: number | null;
  scope: 'all' | 'model' | 'credential';
  model: string;
  auth_index: string;
  confidence: 'confirmed' | 'probable' | 'unverified';
  original_text: string;
}

export interface ResetAnnouncement extends Omit<ResetAnnouncementInput, 'source'> {
  id: string;
  source: 'manual' | 'status_rss' | 'x_api';
  registered_at_ms: number;
  created_at_ms: number;
  status: 'active' | 'expired';
}

export interface ResetAnnouncementPage {
  items: ResetAnnouncement[];
  total: number;
}
