I am now designing the API layer on top of the golang database that has already been created and dockerized.

I would to come up with a golang HTTP JSON API in Go with three endpoints:
   1. GET available times for a trainer between two dates (trainer_id, starts_at, ends_at).
   2. POST a new appointment as JSON (trainer_id, user_id, starts_at, ends_at).
   3. GET scheduled appointments for a trainer (trainer_id).

All of these should include as mentionned in AGENTS.md:
- unit tests
- observability
- endpoint documentation
