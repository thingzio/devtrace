-- Migration 016: default new tenants to the Pro plan.
-- During the beta preview the Pro plan is free for all users, so new
-- signups should land on Pro automatically rather than starting on Free
-- and being upgraded by hand. Existing tenants are not modified — admin
-- upgrades them out of band.

ALTER TABLE devtrace_tenant
    ALTER COLUMN plan SET DEFAULT 'pro';

ALTER TABLE devtrace_tenant
    ALTER COLUMN max_contributors SET DEFAULT 2000;
