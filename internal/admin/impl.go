package admin

var (
	_ userStore    = (*UserRepo)(nil)
	_ groupStore   = (*GroupRepo)(nil)
	_ userService  = (*Service)(nil)
	_ groupService = (*GroupService)(nil)
)
