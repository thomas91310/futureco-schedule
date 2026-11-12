# Local Postgres only. First start of an empty volume runs
# /docker-entrypoint-initdb.d/001_schema.sql.
FROM postgres:18

ENV POSTGRES_USER=futureco
ENV POSTGRES_PASSWORD=futureco
ENV POSTGRES_DB=futureco

COPY schema.sql /docker-entrypoint-initdb.d/001_schema.sql
