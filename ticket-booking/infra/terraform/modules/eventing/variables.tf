variable "name_prefix" {
  type = string
}

variable "function_arns" {
  type = map(string) # from modules/compute's function_arns output
}
