// Credential endpoints return data directly. Fetch can reject a redirect
// before the browser repeats a credential-bearing request at another URL.
export const credentialRequestOptions = {
  adapter: 'fetch',
  fetchOptions: { redirect: 'error' },
} as const
