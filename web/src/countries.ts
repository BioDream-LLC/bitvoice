// Countries BitVoice knows tones and emergency numbers for, plus common ones that use the defaults.
export const countryCodes = ['US', 'CA', 'MX', 'GB', 'IE', 'AU', 'NZ', 'DE', 'FR', 'ES', 'IT', 'NL', 'BE', 'AT', 'CH', 'SE', 'NO',
  'DK', 'FI', 'PT', 'PL', 'IN', 'JP', 'CN', 'BR', 'ZA']

const names = new Intl.DisplayNames(['en'], { type: 'region' })
export const countryName = (c: string) => names.of(c) ?? c

/** The browser's country, if it is one of ours. */
export function guessCountry() {
  const region = new Intl.Locale(navigator.language).maximize().region
  return region && countryCodes.includes(region) ? region : 'US'
}

const emergency: Record<string, string[]> = {
  US: ['911', '933'], CA: ['911'], MX: ['911'], GB: ['999', '112'], IE: ['999', '112'], AU: ['000', '112'], NZ: ['111'],
  IN: ['112', '100', '101', '102'], JP: ['110', '119'], CN: ['110', '119', '120'], BR: ['190', '192', '193'], ZA: ['10111', '112'],
}
export const emergencyFor = (c: string) => emergency[c] ?? ['112']
