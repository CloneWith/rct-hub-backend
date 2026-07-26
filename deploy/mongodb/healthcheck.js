/**
 * MongoDB service health check script.
 *
 * This script should be executed via mongosh instead of normal Node.js:
 *   mongosh "mongodb://localhost:27017" --file deploy/mongodb/healthcheck.js
 *
 * db, rs, quit, print are built-in global objects of mongosh.
 * For details see: https://www.mongodb.com/docs/mongodb-shell/write-scripts/
 */

const hello = db.hello();

if (hello.isWritablePrimary) {
  quit(0);
}

try {
  rs.status();
  quit(1);
} catch (error) {
  if (error.codeName !== "NotYetInitialized") {
    print(error);
    quit(2);
  }
}

const result = rs.initiate({
  _id: "rs0",
  members: [{ _id: 0, host: "localhost:27017" }],
});

quit(result.ok === 1 ? 1 : 2);
