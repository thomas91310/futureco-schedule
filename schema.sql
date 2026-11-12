create table trainers (
  id bigint generated always as identity primary key,
  first_name text not null,
  last_name text not null,
  dob date not null,
  constraint trainers_first_name_not_blank check (btrim(first_name) <> ''),
  constraint trainers_last_name_not_blank check (btrim(last_name) <> '')
);

create table users (
  id bigint generated always as identity primary key,
  first_name text not null,
  last_name text not null,
  dob date not null,
  constraint users_first_name_not_blank check (btrim(first_name) <> ''),
  constraint users_last_name_not_blank check (btrim(last_name) <> '')
);

create table appointments (
  id bigint generated always as identity primary key,
  trainer_id bigint not null references trainers (id) on delete restrict,
  user_id bigint not null references users (id) on delete restrict,
  started_at timestamptz not null,
  ended_at timestamptz not null,
  constraint appointments_duration_30_minutes
    check (ended_at = started_at + interval '30 minutes'),
  constraint appointments_started_at_half_hour
    check ((extract(epoch from started_at) % 1800) = 0),
  constraint appointments_trainer_started_at_key
    unique (trainer_id, started_at)
);

create index appointments_user_id_idx on appointments (user_id);
