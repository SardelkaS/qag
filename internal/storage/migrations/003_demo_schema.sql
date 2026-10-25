CREATE SCHEMA qag_demo;
CREATE TABLE qag_demo.items (
    id text PRIMARY KEY,
    name text NOT NULL,
    status text NOT NULL,
    amount numeric(30,3) NOT NULL
);
INSERT INTO qag_demo.items(id,name,status,amount)
VALUES ('example-001','Локальный пример qag','ready',9007199254740993.125);
