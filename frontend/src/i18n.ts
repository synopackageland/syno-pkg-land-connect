const bundles = import.meta.glob('../../i18n/messages/*.json', {eager:true, import:'default'}) as Record<string,Record<string,string>>
import type {UiLocale} from '@synopackageland/ui'
import {labelSeparator} from '@synopackageland/sdk'
const aliases:Record<string,string>={cht:'zh-TW',chs:'zh-CN',enu:'en',jpn:'ja',krn:'ko',ger:'de',fre:'fr',ita:'it',spn:'es'}
const normalize=(locale:string)=>aliases[locale]||locale.replace('_','-')
const uiLocales:string[]=['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']
// The locale the UI library speaks for a DSM language code; unknown codes fall back to English like translate().
export function uiLocale(locale:string):UiLocale {
 const normalized=normalize(locale)
 return (uiLocales.includes(normalized)?normalized:'en') as UiLocale
}
export function translate(locale:string,key:string):string {
 const normalized=normalize(locale)
 const lang=bundles[`../../i18n/messages/${normalized}.json`]||bundles[`../../i18n/messages/${normalized.split('-')[0]}.json`]||bundles['../../i18n/messages/en.json']
 return lang[key]||bundles['../../i18n/messages/en.json'][key]||key
}
// A label and its value, joined as the locale writes them (full-width colon in Chinese and Japanese).
export function labelled(locale:string,key:string,value:string):string {
 return translate(locale,key)+labelSeparator(uiLocale(locale))+value
}
