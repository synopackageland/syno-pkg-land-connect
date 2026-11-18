import {dsmLoginSetup} from '@synopackageland/cli/e2e';

// An ordinary DSM user in its own session, for the admin-only boundary.
dsmLoginSetup({session: 'dsm-normal1', user: 'normal1'});
