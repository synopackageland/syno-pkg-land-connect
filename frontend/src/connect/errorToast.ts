// A DSM refusal always keeps its recovery action, even inside a toast.
import type {ToastController} from '@synopackageland/ui'
import type {ConnectState} from './state'
import {translate} from '../i18n'
export function failureToast(toast:ToastController,s:ConnectState,title:string,error:unknown){
 const description=s.message(error)
 if((error as {code?:string})?.code==='pin_proxy_required')return toast.show({title,description,tone:'danger',actionLabel:translate(s.ctx.locale,'apps.dsmReview.action'),onAction:()=>s.dsmReviewAction.value?.()})
 return toast.danger(title,description)
}
