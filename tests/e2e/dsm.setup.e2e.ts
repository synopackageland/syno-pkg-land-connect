import {dsmLoginSetup} from '@synopackageland/cli/e2e';

// Signs admin in once per run; a saved profile that still reaches the desktop is reused.
dsmLoginSetup({session: 'dsm', user: 'admin'});
