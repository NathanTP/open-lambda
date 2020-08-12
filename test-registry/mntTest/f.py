import pathlib
import os

def f(event):
    mntPath = pathlib.Path("/shared")
    hostF = mntPath / "host2lambda"
    lbdF = mntPath / "lambda2host"

    uid = os.getuid()
    gid = os.getgid()

    try:
       stat = hostF.stat()
    except Exception as e:
       return "Error when stating host2lambda: " + str(e)

    if stat.st_uid != uid or stat.st_gid != gid:
       return "Wrong uid:gid on host2lambda: expected {}:{}, got {}:{}".format(uid, gid, stat.st_uid, stat.st_gid)
    
    try:
        lbdF.touch(0o600)
    except Exception as e:
        return "Error when creating lambda2host: " + str(e)

    return "Success\n"
