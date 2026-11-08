-- +goose Up
-- time.week.unsubmitted (virtual employees design V30): a person's week that has
-- ended and was not submitted is announced once. This table is the record of what
-- was announced, and the row the event is published under.
CREATE TABLE time.week_announced (
    user_id      uuid        NOT NULL,
    week_start   date        NOT NULL,
    announced_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, week_start)
);

-- +goose Down
DROP TABLE time.week_announced;
