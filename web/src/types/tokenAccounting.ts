export type TokenAccountingStatus = 'legacy' | 'complete' | 'unsupported' | 'invalid' | 'inconsistent' | 'unclassified';

interface TokenAccountingEvidence {
  version: number;
  raw_tokens: {
    input_tokens: number;
    output_tokens: number;
    reasoning_tokens: number;
    cached_tokens: number;
    cache_read_tokens: number;
    cache_read_tokens_present: boolean;
    cache_creation_tokens: number;
    total_tokens: number;
    total_tokens_present?: boolean;
  };
}

export interface CanonicalTokenBreakdown {
  schema_version: number;
  quality: string;
  total_tokens: number;
  input: { total_tokens: number; uncached_tokens: number; cache_read_tokens: number; cache_write_tokens: number };
  output: { total_tokens: number; non_reasoning_tokens: number; reasoning_tokens: number };
  unclassified_tokens: number;
}

export type TokenAccounting = TokenAccountingEvidence & (
  | { status: 'complete'; token_breakdown: CanonicalTokenBreakdown }
  | { status: Exclude<TokenAccountingStatus, 'complete'>; token_breakdown?: CanonicalTokenBreakdown }
);

/**
 * Whether the record cannot be priced from its counts at all.
 *
 * Legacy evidence is billable: it preserves the original totals and the pricing
 * path accepts it. Only a record whose counts could not be reconciled is
 * excluded, and changing the model's price is no remedy for that.
 */
export function isUnresolvedAccounting(accounting?: TokenAccounting | null): boolean {
  return accounting != null && accounting.status !== 'complete' && accounting.status !== 'legacy';
}
