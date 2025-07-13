type MyClass {
  pub value = 0

  pub new(value: Int!): MyClass! {
    self.value = value
    self
  }

  pub add(value: Int!): MyClass! {
    self.value += value
    self
  }

  pub currentValue: Int! {
    serverInfo.totalPosts + self.value
  }
}

MyClass.new(42).currentValue
