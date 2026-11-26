export function highlightMatch(text, query) {
  if (!query || !query.trim() || !text) return [{ text, highlight: false }];
  
  const searchTerms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (searchTerms.length === 0) return [{ text, highlight: false }];

  const regex = new RegExp(`(${searchTerms.map(t => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'gi');
  
  const segments = [];
  let lastIndex = 0;
  let match;

  while ((match = regex.exec(text)) !== null) {
    if (match.index > lastIndex) {
      segments.push({ text: text.substring(lastIndex, match.index), highlight: false });
    }
    segments.push({ text: match[0], highlight: true });
    lastIndex = match.index + match[0].length;
    // Prevent infinite loops with zero-length matches
    if (regex.lastIndex === match.index) {
      regex.lastIndex++;
    }
  }

  if (lastIndex < text.length) {
    segments.push({ text: text.substring(lastIndex), highlight: false });
  }

  return segments;
}
