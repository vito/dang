# Test GraphQL directive declarations and applications

# Test basic directive declaration
directive @deprecated(reason: String = "No longer supported") on FIELD_DEFINITION | OBJECT

# Test simple directive with no arguments
directive @experimental on FIELD_DEFINITION | ARGUMENT_DEFINITION

# Test directive with required argument
directive @auth(role: String!) on FIELD_DEFINITION

# Test directive with multiple arguments
directive @cache(ttl: Int! = 300, key: String) on FIELD_DEFINITION

# Test directive applications on type declarations
type Person @deprecated(reason: "Use NewPerson instead") {
  pub id: String!
  pub name: String! @deprecated(reason: "Use displayName instead")
  pub displayName: String!
  pub email: String! @experimental
  pub adminData: String! @auth(role: "admin")
  pub cachedData: String! @cache(ttl: 60, key: "user_cache")
}

# Test directive application on function arguments
pub processUser(user: Person! @experimental): String! {
  user.displayName
}

print("Directive tests passed!")