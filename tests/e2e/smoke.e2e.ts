import {appOpenSmoke} from '@synopackageland/cli/e2e';

// A (admin) reaches the Overview; N (a normal user) gets the admin-only
// denial; X (signed out) gets the DSM sign-in.
appOpenSmoke({ready: '[data-page="status"]'});
