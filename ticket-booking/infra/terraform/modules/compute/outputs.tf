output "function_arns" {
  value = { for k, f in aws_lambda_function.fn : k => f.arn }
}

output "function_invoke_arns" {
  value = { for k, f in aws_lambda_function.fn : k => f.invoke_arn }
}

output "server_function_name" {
  value = aws_lambda_function.fn["server"].function_name
}

output "lambda_exec_role_arn" {
  value = aws_iam_role.lambda_exec.arn
}
