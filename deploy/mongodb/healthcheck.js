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
