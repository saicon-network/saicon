-- Every row carries chain_id. The devnet will be reset; a naive height cursor
-- would then splice two chains into one series.
CREATE TABLE IF NOT EXISTS chains (
    chain_id            TEXT PRIMARY KEY,
    indexed_height      INTEGER NOT NULL DEFAULT 0,
    -- The lowest height the follower actually walked from. 1 normally; higher
    -- against a pruned node. Without it the cursor alone would be read as proof
    -- of having walked, and a seeded cursor is not that.
    first_walked_height INTEGER NOT NULL DEFAULT 1,
    -- The epoch length every row in this database was bucketed under. It is a
    -- governance parameter, and a change renumbers the chain's epochs from that
    -- height on. Comparing it only against a value held in memory made the check
    -- survive exactly as long as the process did: stop the service, change the
    -- parameter, start it again, and the new length silently becomes the baseline
    -- while proposals_raw still holds rows bucketed under the old one.
    epoch_length INTEGER,
    -- The earliest block the node still retained when first seen, not the
    -- chain's genesis time: every non-archive node prunes.
    earliest_block_time TEXT,
    first_seen          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

-- Nullable wherever the chain may decline to answer. NULL means "no record";
-- 0 means the chain measured zero. They are different facts.
CREATE TABLE IF NOT EXISTS validators (
    chain_id             TEXT NOT NULL,
    operator             TEXT NOT NULL,
    moniker              TEXT,
    staking_status       TEXT,
    jailed               INTEGER,
    tokens               TEXT,
    commission_rate      TEXT,
    registry_status      TEXT,
    compliance_flag      INTEGER,
    bonding_start        TEXT,
    bonding_start_height INTEGER,
    lockup_seconds       INTEGER,
    accumulated_ep       TEXT,
    last_epoch_update    INTEGER,
    updated_at           TEXT NOT NULL,
    PRIMARY KEY (chain_id, operator)
);

CREATE TABLE IF NOT EXISTS epoch_snapshots (
    chain_id        TEXT NOT NULL,
    epoch           INTEGER NOT NULL,
    operator        TEXT NOT NULL,
    computed_score  TEXT,
    computed_at     TEXT,
    blocks_signed   INTEGER,
    blocks_missed   INTEGER,
    reward_amount   TEXT,
    reward_eligible INTEGER,
    reward_reason   TEXT,
    proposals       INTEGER,
    PRIMARY KEY (chain_id, epoch, operator)
);

-- Keyed by consensus address: that is what a block header carries. The epoch
-- reader resolves it to an operator address afterwards, so a block can be
-- counted before its validator is known.
CREATE TABLE IF NOT EXISTS proposals_raw (
    chain_id  TEXT NOT NULL,
    epoch     INTEGER NOT NULL,
    cons_addr TEXT NOT NULL,
    count     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (chain_id, epoch, cons_addr)
);

-- Without this the scoreboard's per-validator MAX(epoch) subquery can only walk
-- the primary key backwards from the newest epoch until it meets that operator —
-- one step for a validator still being written, the whole history for one that
-- left. Measured on 75 validators x 6300 epochs with 20 departed: 715 ms, and
-- 1 ms with this index. Every /v1/validators/{op} request pays it too.
CREATE INDEX IF NOT EXISTS idx_snapshots_operator
    ON epoch_snapshots (chain_id, operator, epoch);

-- An epoch appears here only when every validator's row for it was written. A
-- partially written epoch must not look finished: ReadEpoch is not atomic across
-- validators, and taking MAX(epoch) from the snapshots themselves marks an epoch
-- done as soon as the FIRST validator lands. On a 75-validator epoch that is
-- roughly 300 REST calls, so a failure part-way through is the likely case, and
-- the validators after the failure would have no row for that epoch for ever.
CREATE TABLE IF NOT EXISTS epochs_read (
    chain_id     TEXT NOT NULL,
    epoch        INTEGER NOT NULL,
    completed_at TEXT NOT NULL,
    PRIMARY KEY (chain_id, epoch)
);
