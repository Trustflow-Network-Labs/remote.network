import { faker } from '@faker-js/faker'

export function useTextUtils() {
  const shorten = (text: string, prefixLen: number, suffixLen: number): string => {
    if (!text || text.length <= prefixLen + suffixLen) {
      return text // If the string is already short, return as is
    }
    const prefix = text.slice(0, prefixLen)
    const suffix = suffixLen > 0 ? text.slice(-suffixLen) : ''
    return `${prefix}...${suffix}`
  }

  const generateRandomName = (): string => {
    return `${faker.word.adjective()}-${faker.animal.type()}-${Date.now().toString(36).slice(-4)}`
  }

  return {
    shorten,
    generateRandomName
  }
}
