import {mount} from './main'
import {httpContext} from './transport'
mount(document.querySelector<HTMLElement>('#app')!,httpContext('/api.cgi'))
