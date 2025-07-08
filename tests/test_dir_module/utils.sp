# Utility functions using types from types.sp
pub createPerson(name: String!, age: Int!): Person! {
  Person.new(name, age)
}

pub getMagicNumber(): Int! {
  MAGIC_NUMBER
}

pub formatVersion(): String! {
  "Version: " + VERSION
}
