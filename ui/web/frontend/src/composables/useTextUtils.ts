export function useTextUtils() {
  const shorten = (text: string, prefixLen: number, suffixLen: number): string => {
    if (!text || text.length <= prefixLen + suffixLen) {
      return text // If the string is already short, return as is
    }
    const prefix = text.slice(0, prefixLen)
    const suffix = suffixLen > 0 ? text.slice(-suffixLen) : ''
    return `${prefix}...${suffix}`
  }

  return {
    shorten
  }
}
