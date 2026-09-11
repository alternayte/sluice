-- Demo source data of the ELT example: 3 customers and 5 orders in the schema source.
-- Run it as the owner of the warehouse database. compose.yml runs it at the first start.
CREATE SCHEMA source;

CREATE TABLE source.customers (
    id integer PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE source.orders (
    id integer PRIMARY KEY,
    customer_id integer NOT NULL,
    amount numeric(10, 2) NOT NULL
);

INSERT INTO source.customers VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Cy');

INSERT INTO source.orders VALUES
    (1, 1, 10.00),
    (2, 1, 5.50),
    (3, 2, 7.25),
    (4, 2, 1.00),
    (5, 2, 2.25);
