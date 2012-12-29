package org.gaffo.jorb.acjtest;

import org.gaffo.jorb.ACJ;
import org.gaffo.jorb.ACJTest;

public class Job2 extends ACJ
{
    private ACJTest callback;

    public Job2(ACJTest callback)
    {
        this.callback = callback;
    }

    public void process(I2 i2, I1 i1, String string)
    {
        callback.verifyCallback(i1);
        callback.verifyCallback(i2);
        callback.verifyCallback(string);
    }
}
