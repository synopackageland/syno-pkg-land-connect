import en from './i18n/en.json'
import zhTW from './i18n/zh-TW.json'
import zhCN from './i18n/zh-CN.json'
import ja from './i18n/ja.json'
import ko from './i18n/ko.json'
import de from './i18n/de.json'
import fr from './i18n/fr.json'
import es from './i18n/es.json'
import it from './i18n/it.json'

export type ShellCopy = typeof en
export const shellCatalogs: Record<string, ShellCopy> = {en,'zh-TW':zhTW,'zh-CN':zhCN,ja,ko,de,fr,es,it}
const aliases: Record<string,string> = {enu:'en',cht:'zh-TW',chs:'zh-CN',jpn:'ja',krn:'ko',ger:'de',fre:'fr',spn:'es',ita:'it'}
export function shellCopy(locale: string): ShellCopy {
  const normalized = locale.replaceAll('_','-')
  return shellCatalogs[aliases[normalized] ?? normalized] ?? shellCatalogs[normalized.split('-')[0]!] ?? en
}
