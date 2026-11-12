I am designing a database schema for a golang API.
I would like only to write the database schema for now.

For the database schema:
I have identified 3 entities: 
- trainers
- users
- appointments

I think of a simple design with the appointments table simply using two foreign keys to users and trainers tables for creation of appointments.

REQUIREMENTS:
- Clients pick from a list of available times, and a trainer's appointments can't overlap.
- Every appointment is 30 minutes and starts at :00 or :30.

Can you read @AGENTS.md and come up with a plan for:

- the actual schema.sql for this
- the Dockerfile needed for local development
- the load script that loads `appointments.json`.
- the load script for now, whenever it meets an interval longer than 30 minutes will ingest it as multiple 30 minutes appointments. (1 hour is two 30 minutes appointments for example).
- You can add this setup script in `scripts/`, start local db, run load script so it's setup and has data
