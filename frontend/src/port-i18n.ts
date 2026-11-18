import en from '../../i18n/messages/en.json'
import zhTW from '../../i18n/messages/zh-TW.json'
import zhCN from '../../i18n/messages/zh-CN.json'
import ja from '../../i18n/messages/ja.json'
import ko from '../../i18n/messages/ko.json'
import de from '../../i18n/messages/de.json'
import fr from '../../i18n/messages/fr.json'
import es from '../../i18n/messages/es.json'
import it from '../../i18n/messages/it.json'
const catalogs:Record<string,Record<string,string>>={en,'zh-TW':zhTW,'zh-CN':zhCN,ja,ko,de,fr,es,it}
// Accept both SDK BCP47 locale names and DSM language codes; unknown -> English.
const aliases:Record<string,string>={enu:'en',cht:'zh-TW',chs:'zh-CN',jpn:'ja',krn:'ko',ger:'de',fre:'fr',spn:'es',ita:'it'}
export function portText(locale:string,key:string):string {
 const name=aliases[locale]||Object.keys(catalogs).find(k=>k.toLowerCase()===locale.toLowerCase())||'en'
 return catalogs[name]?.[key]||en[key as keyof typeof en]||key
}
