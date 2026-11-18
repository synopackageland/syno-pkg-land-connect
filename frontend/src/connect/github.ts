// GitHub refusals the page explains and turns into "Add a GitHub token".
// The token page opens GitHub's fine-grained token form prefilled through its
// documented query parameters (name, description, expires_in); with no
// permission parameters the token reads public repositories only, which is
// all release checks and downloads need, so the user only clicks Generate.
export const githubTokenUrl='https://github.com/settings/personal-access-tokens/new?'+new URLSearchParams({
 name:'Syno Connect Center',
 description:'Raises the GitHub rate limit for Syno Connect Center update checks and downloads. No extra permissions needed',
 expires_in:'365',
}).toString()

export type GitHubCode='github_rate_limited'|'github_token_invalid'
export const isGitHubCode=(code:unknown):code is GitHubCode=>code==='github_rate_limited'||code==='github_token_invalid'

// The text for a GitHub refusal: when the limit resets, or that the token is bad.
export function githubText(translate:(key:string)=>string,format:(at:string)=>string,code:GitHubCode,retryAt?:string):string{
 if(code==='github_token_invalid')return translate('binaries.tokenInvalid')
 return retryAt?translate('binaries.rateLimited').replace('{time}',format(retryAt)):translate('binaries.rateLimitedLater')
}
