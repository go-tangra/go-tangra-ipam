// PUT endpoints replace the whole record, so an edit dialog that only covers
// some fields must send the untouched ones back too. Fields the form cleared
// (undefined after the schema's blank→undefined transform) go out as null so
// the server stores them empty instead of keeping the old value.
export function mergeEdit(original: object, values: Record<string, unknown>, keys: readonly string[]): Record<string, unknown> {
  const out: Record<string, unknown> = { ...original }
  for (const k of keys) out[k] = values[k] ?? null
  return out
}
