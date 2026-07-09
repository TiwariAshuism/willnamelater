/** Split a plain string into individual words for staggered reveal. */
export function words(text: string): string[] {
  return text.trim().split(/\s+/);
}
