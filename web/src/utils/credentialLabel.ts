/** Remove only opaque identity prefixes; the original value remains the API target. */
export function credentialLabel(value: string | undefined, opaqueKeys: Array<string | undefined>, fallback = ''): string {
  let label = (value ?? '').trim();
  const keys = opaqueKeys.filter((key): key is string => Boolean(key)).sort((left, right) => right.length - left.length);
  for (const key of keys) {
    if (label === key) return fallback;
    if (label.startsWith(key) && /^[\s:|/()[\]_-]/.test(label.slice(key.length))) {
      label = label.slice(key.length).replace(/^[\s:|/()[\]_-]+/, '').trim();
    }
  }
  label = label.replace(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:$|[\s:|/()[\]_-]+)/i, '').trim();
  return label || fallback;
}
