CREATE TABLE operational_incidents (
 model TEXT NOT NULL,
 protocol TEXT NOT NULL,
 cause TEXT NOT NULL,
 PRIMARY KEY(model, protocol, cause)
);
