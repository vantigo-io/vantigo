/**
 * The prefix of the signed-in user's profile query, which the host's shell
 * reads for the header's avatar and its Profile page for the form: the host
 * keys it by user under this prefix. An app that changes the user's own
 * avatar elsewhere (Staff's My page) invalidates the prefix, so the header
 * shows the new picture without importing the host.
 */
export const PROFILE_QUERY_KEY = ["account", "profile"] as const;
